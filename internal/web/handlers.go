package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lendradev/tugas-submission/internal/auth"
	"github.com/lendradev/tugas-submission/internal/limiter"
	"github.com/lendradev/tugas-submission/internal/pathsafe"
	"github.com/lendradev/tugas-submission/internal/store"
)

// ---------------------------------------------------------------- login

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.auth.Resolve(s.cookieSession(r)); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login.html", map[string]any{
		"Error": "",
		"CSRF":  "",
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := limiter.ClientIP(r, s.cfg.TrustedProxy)
	username := strings.TrimSpace(strings.ToLower(r.FormValue("username")))
	password := r.FormValue("password")

	// Batasi percobaan per IP dan per username agar tidak bisa di-brute force.
	ipKey := "login:ip:" + ip
	userKey := "login:user:" + username
	if !s.limitLogin.Allow(ipKey) {
		s.tooMany(w, r, s.limitLogin, ipKey)
		return
	}
	if !s.limitLogin.Allow(userKey) {
		s.tooMany(w, r, s.limitLogin, userKey)
		return
	}
	if username == "" || password == "" {
		s.render(w, http.StatusBadRequest, "login.html", map[string]any{
			"Error": "username dan password wajib diisi",
		})
		return
	}

	sess, err := s.auth.Login(username, password)
	if err != nil {
		msg := "username atau password salah"
		if errors.Is(err, auth.ErrInactive) {
			msg = "akun ini dinonaktifkan. Hubungi admin."
		}
		s.render(w, http.StatusUnauthorized, "login.html", map[string]any{"Error": msg})
		return
	}
	s.limitLogin.Reset(ipKey)
	s.limitLogin.Reset(userKey)
	s.setSessionCookie(w, sess)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if id := s.cookieSession(r); id != "" {
		_ = s.auth.Logout(id)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------------------------------------------------------------- halaman

func (s *Server) handleFilesPage(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	csrf, _ := r.Context().Value(ctxCSRF).(string)
	s.render(w, http.StatusOK, "files.html", map[string]any{
		"User":      publicUser(user),
		"CSRF":      csrf,
		"MaxUpload": s.Settings().MaxUploadSizeMB,
		"QuotaMB":   s.Settings().TotalQuotaMB,
		"RepoReady": s.pusher != nil && s.pusher.Ready(),
		"RepoDesc":  s.repoDesc(),
	})
}

// publicUser mengembalikan data pengguna yang aman dikirim ke browser.
// Hash password tidak pernah masuk ke HTML.
func publicUser(u store.User) map[string]any {
	return map[string]any{
		"id":           u.ID,
		"username":     u.Username,
		"display_name": u.DisplayName,
		"role":         u.Role,
	}
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	csrf, _ := r.Context().Value(ctxCSRF).(string)
	s.render(w, http.StatusOK, "admin.html", map[string]any{
		"User": publicUser(user),
		"CSRF": csrf,
	})
}

// handleAdminListUser mengembalikan daftar pengguna beserta statistiknya.
func (s *Server) handleAdminListUser(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal membaca pengguna")
		return
	}
	states, err := s.store.SyncStateUsers()
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal membaca status sinkronisasi")
		return
	}
	stateByUser := map[int64]store.SyncState{}
	for _, st := range states {
		stateByUser[st.UserID] = st
	}

	out := []map[string]any{}
	totalFiles := 0
	totalPending := 0
	for _, u := range users {
		count, _ := s.store.CountFiles(u.ID)
		pending, _ := s.store.PendingFiles(u.ID)
		size, _ := s.store.UserSize(u.ID)
		totalFiles += count
		totalPending += len(pending)
		last := ""
		if st, ok := stateByUser[u.ID]; ok && st.LastPushAt != nil {
			last = st.LastPushAt.Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"id":            u.ID,
			"username":      u.Username,
			"display_name":  u.DisplayName,
			"role":          u.Role,
			"active":        u.Active,
			"file_count":    count,
			"pending_count": len(pending),
			"size":          size,
			"last_push_at":  last,
		})
	}
	totalSize, _ := s.store.TotalSize()
	s.jsonOK(w, map[string]any{
		"users": out,
		"stats": map[string]any{
			"user_count": len(users),
			"file_count": totalFiles,
			"total_size": totalSize,
			"pending":    totalPending,
		},
	})
}

// handleAdminGetSettings mengembalikan pengaturan saat ini.
func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	set := s.Settings()
	s.jsonOK(w, map[string]any{
		"settings": map[string]any{
			"max_upload_size_mb":    set.MaxUploadSizeMB,
			"total_quota_mb":        set.TotalQuotaMB,
			"max_files_per_request": set.MaxFilesPerReq,
			"debounce_base_min":     int(set.Debounce.Base.Minutes()),
			"debounce_min_min":      int(set.Debounce.Min.Minutes()),
			"debounce_max_min":      int(set.Debounce.Max.Minutes()),
			"burst_window_min":      int(set.Debounce.BurstWindow.Minutes()),
		},
	})
}

// ---------------------------------------------------------------- list

type listItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Status  string `json:"status"`
	Updated string `json:"updated"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	dir := r.URL.Query().Get("path")
	if dir != "" {
		cleaned, err := pathsafe.Clean(dir)
		if err != nil {
			s.jsonError(w, http.StatusBadRequest, "path tidak valid")
			return
		}
		dir = cleaned
	}

	files, err := s.store.AllFiles(user.ID)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal membaca daftar berkas")
		return
	}

	items := []listItem{}
	seenDirs := map[string]bool{}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	for _, f := range files {
		if prefix != "" {
			if !strings.HasPrefix(f.Path, prefix) {
				continue
			}
			rest := strings.TrimPrefix(f.Path, prefix)
			if rest == "" {
				continue
			}
			if i := strings.Index(rest, "/"); i >= 0 {
				sub := prefix + rest[:i]
				if !seenDirs[sub] {
					seenDirs[sub] = true
					items = append(items, listItem{Name: rest[:i], Path: sub, IsDir: true})
				}
				continue
			}
		} else {
			// Di root, berkas di dalam folder ditampilkan sebagai entri folder.
			if i := strings.Index(f.Path, "/"); i >= 0 {
				sub := f.Path[:i]
				if !seenDirs[sub] {
					seenDirs[sub] = true
					items = append(items, listItem{Name: sub, Path: sub, IsDir: true})
				}
				continue
			}
		}
		items = append(items, listItem{
			Name:    pathsafe.Base(f.Path),
			Path:    f.Path,
			Size:    f.Size,
			Status:  f.Status,
			Updated: f.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return items[i].Name < items[j].Name
	})

	total := int64(0)
	for _, f := range files {
		total += f.Size
	}
	s.jsonOK(w, map[string]any{
		"items":      items,
		"path":       dir,
		"total_size": total,
	})
}

// ---------------------------------------------------------------- status

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	csrf, _ := r.Context().Value(ctxCSRF).(string)

	pending, err := s.store.PendingFiles(user.ID)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal membaca status sinkronisasi")
		return
	}
	states, err := s.store.SyncStateUsers()
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal membaca status sinkronisasi")
		return
	}
	var st store.SyncState
	for _, x := range states {
		if x.UserID == user.ID {
			st = x
			break
		}
	}
	used, err := s.store.UserSize(user.ID)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal menghitung ukuran")
		return
	}
	count, _ := s.store.CountFiles(user.ID)

	next := ""
	if st.NextPushAt != nil {
		next = st.NextPushAt.Format(time.RFC3339)
	}
	s.jsonOK(w, map[string]any{
		"csrf":          csrf,
		"pending":       len(pending),
		"next_push_at":  next,
		"last_push_at":  timeStr(st.LastPushAt),
		"last_error":    st.LastError,
		"used_bytes":    used,
		"quota_bytes":   int64(s.Settings().TotalQuotaMB) << 20,
		"file_count":    count,
		"max_upload_mb": s.Settings().MaxUploadSizeMB,
	})
}

func (s *Server) handleUserSettings(w http.ResponseWriter, r *http.Request) {
	set := s.Settings()
	s.jsonOK(w, map[string]any{
		"max_upload_size_mb": set.MaxUploadSizeMB,
		"total_quota_mb":     set.TotalQuotaMB,
		"max_files_per_req":  set.MaxFilesPerReq,
		"debounce_base_min":  int(set.Debounce.Base.Minutes()),
	})
}

func timeStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// ---------------------------------------------------------------- upload

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	if !s.requireCSRF(w, r) {
		return
	}
	ip := limiter.ClientIP(r, s.cfg.TrustedProxy)
	if !s.limitUpload.Allow(ip) {
		s.tooMany(w, r, s.limitUpload, ip)
		return
	}
	userKey := fmt.Sprintf("upload:user:%d", user.ID)
	if !s.limitUpload.Allow(userKey) {
		s.tooMany(w, r, s.limitUpload, userKey)
		return
	}

	set := s.Settings()
	maxBytes := int64(set.MaxUploadSizeMB) << 20
	maxTotal := int64(set.TotalQuotaMB) << 20

	used, err := s.store.UserSize(user.ID)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal menghitung kuota")
		return
	}

	// Batasi jumlah berkas dan ukuran total permintaan.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes*int64(set.MaxFilesPerReq)+1<<20)

	mr, err := r.MultipartReader()
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "permintaan upload tidak valid")
		return
	}

	// Path folder tujuan dikirim sebagai query parameter, bukan part multipart.
	//
	// Alasannya: multipart.Reader bersifat streaming — memanggil NextPart()
	// membuang sisa isi part sebelumnya. Karena itu field path tidak boleh
	// bergantung pada urutan part, dan berkas harus langsung ditulis ke disk
	// saat part-nya dibaca.
	dir := r.URL.Query().Get("path")
	if dir != "" {
		cleaned, cerr := pathsafe.Clean(dir)
		if cerr != nil {
			s.jsonError(w, http.StatusBadRequest, "path folder tidak valid")
			return
		}
		dir = cleaned
	}

	staging := s.stagingDir(user.ID)
	if err := ensureDir(staging); err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal menyiapkan folder staging")
		return
	}

	// Batasi jumlah unggahan bersamaan untuk menjaga RAM/CPU server kecil.
	select {
	case s.uploadSem <- struct{}{}:
		defer func() { <-s.uploadSem }()
	default:
		s.jsonError(w, http.StatusServiceUnavailable, "server sedang sibuk, coba lagi sebentar")
		return
	}

	saved := []string{}
	var totalAdded int64
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.jsonError(w, http.StatusBadRequest, "permintaan upload terputus")
			return
		}
		if part.FormName() != "files" {
			_ = part.Close()
			continue
		}
		if len(saved) >= set.MaxFilesPerReq {
			_ = part.Close()
			s.jsonError(w, http.StatusBadRequest, fmt.Sprintf("maksimal %d berkas per permintaan", set.MaxFilesPerReq))
			return
		}

		name, rel, size, serr := s.saveUploadPart(part, dir, staging, maxBytes)
		_ = part.Close()
		if serr != nil {
			if errors.Is(serr, errTooLarge) {
				s.jsonError(w, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("berkas %q melebihi batas %d MB", name, set.MaxUploadSizeMB))
				return
			}
			s.jsonError(w, http.StatusBadRequest, serr.Error())
			return
		}
		saved = append(saved, rel)
		totalAdded += size

		// Daftarkan di mirror index supaya file terlihat di UI dan dijadwalkan push.
		if err := s.store.UpsertFile(user.ID, rel, size, store.StatusPending); err != nil {
			s.jsonError(w, http.StatusInternalServerError, "gagal mendaftarkan berkas")
			return
		}
		if err := s.store.LogChange(user.ID); err != nil {
			s.log.Error("catat perubahan", "err", err)
		}
	}

	if len(saved) == 0 {
		s.jsonError(w, http.StatusBadRequest, "tidak ada berkas yang diunggah")
		return
	}
	if used+totalAdded > maxTotal {
		s.jsonError(w, http.StatusInsufficientStorage, "kuota penyimpanan habis")
		return
	}

	// Jadwalkan sinkronisasi sesuai debounce adaptif.
	if s.scheduler != nil {
		if err := s.scheduler.ScheduleNext(user.ID, s.Settings().Debounce); err != nil {
			s.log.Error("jadwalkan push", "err", err)
		}
	}
	s.jsonOK(w, map[string]any{
		"saved": saved,
		"total": totalAdded,
	})
}

var errTooLarge = errors.New("berkas terlalu besar")

// saveUploadPart menulis satu part multipart ke disk dan mendaftarkannya di mirror.
func (s *Server) saveUploadPart(part *multipart.Part, dir, staging string, maxBytes int64) (string, string, int64, error) {
	filename := filepath.Base(part.FileName())
	if filename == "" || filename == "." {
		return "", "", 0, errors.New("nama berkas tidak valid")
	}
	rel, err := pathsafe.Join(dir, filename)
	if err != nil {
		return filename, "", 0, fmt.Errorf("nama berkas %q tidak valid", filename)
	}
	target := filepath.Join(staging, filepath.FromSlash(rel))
	if err := ensureDir(filepath.Dir(target)); err != nil {
		return filename, "", 0, errors.New("gagal membuat folder tujuan")
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return filename, "", 0, errors.New("gagal menyimpan berkas")
	}
	defer f.Close()

	// Batasi jumlah byte: sisakan 1 byte untuk mendeteksi kelebihan.
	written, err := io.Copy(f, io.LimitReader(part, maxBytes+1))
	if err != nil {
		_ = os.Remove(target)
		return filename, "", 0, errors.New("gagal menyimpan berkas")
	}
	if written > maxBytes {
		_ = os.Remove(target)
		return filename, "", 0, errTooLarge
	}
	return filename, rel, written, nil
}

// ---------------------------------------------------------------- rename

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	if !s.requireCSRF(w, r) {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.jsonError(w, http.StatusBadRequest, "payload tidak valid")
		return
	}
	from, err := pathsafe.Clean(body.From)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "path asal tidak valid")
		return
	}
	to, err := pathsafe.Clean(body.To)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "path tujuan tidak valid")
		return
	}
	if from == to {
		s.jsonError(w, http.StatusBadRequest, "nama tidak berubah")
		return
	}

	// Path asal harus milik user dan belum berupa folder.
	rec, err := s.store.FileByPath(user.ID, from)
	if err != nil {
		if isNotFound(err) {
			s.jsonError(w, http.StatusNotFound, "berkas tidak ditemukan")
			return
		}
		s.jsonError(w, http.StatusInternalServerError, "gagal membaca berkas")
		return
	}
	if _, err := s.store.FileByPath(user.ID, to); err == nil {
		s.jsonError(w, http.StatusConflict, "berkas dengan nama itu sudah ada")
		return
	}

	staging := s.stagingDir(user.ID)
	src := filepath.Join(staging, filepath.FromSlash(from))
	dst := filepath.Join(staging, filepath.FromSlash(to))
	if err := ensureDir(filepath.Dir(dst)); err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal menyiapkan folder tujuan")
		return
	}
	if _, err := os.Stat(src); err == nil {
		if err := os.Rename(src, dst); err != nil {
			s.jsonError(w, http.StatusInternalServerError, "gagal mengganti nama berkas")
			return
		}
	}

	// Perbarui mirror: berkas lama dihapus, berkas baru menunggu push.
	_ = s.store.MarkDeleted(user.ID, from)
	_ = s.store.UpsertFile(user.ID, to, rec.Size, store.StatusPending)
	if s.scheduler != nil {
		_ = s.scheduler.ScheduleNext(user.ID, s.Settings().Debounce)
	}
	s.jsonOK(w, map[string]any{"from": from, "to": to})
}

// ---------------------------------------------------------------- delete

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	if !s.requireCSRF(w, r) {
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(r, &body); err != nil {
		s.jsonError(w, http.StatusBadRequest, "payload tidak valid")
		return
	}
	if len(body.Paths) == 0 {
		s.jsonError(w, http.StatusBadRequest, "tidak ada berkas yang dipilih")
		return
	}
	if len(body.Paths) > 100 {
		s.jsonError(w, http.StatusBadRequest, "terlalu banyak berkas dalam satu permintaan")
		return
	}

	staging := s.stagingDir(user.ID)
	deleted := []string{}
	for _, p := range body.Paths {
		clean, err := pathsafe.Clean(p)
		if err != nil {
			s.jsonError(w, http.StatusBadRequest, "path tidak valid")
			return
		}
		if _, err := s.store.FileByPath(user.ID, clean); err != nil {
			if isNotFound(err) {
				continue
			}
			s.jsonError(w, http.StatusInternalServerError, "gagal membaca berkas")
			return
		}
		// Hapus dari staging bila masih ada; penandaan hapus di mirror
		// akan diterapkan ke repo saat sinkronisasi berikutnya.
		_ = os.Remove(filepath.Join(staging, filepath.FromSlash(clean)))
		if err := s.store.MarkDeleted(user.ID, clean); err != nil {
			s.jsonError(w, http.StatusInternalServerError, "gagal menandai berkas")
			return
		}
		deleted = append(deleted, clean)
	}
	if len(deleted) == 0 {
		s.jsonError(w, http.StatusNotFound, "tidak ada berkas yang cocok")
		return
	}
	if s.scheduler != nil {
		_ = s.scheduler.ScheduleNext(user.ID, s.Settings().Debounce)
	}
	s.jsonOK(w, map[string]any{"deleted": deleted})
}

// ---------------------------------------------------------------- download

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxUser).(store.User)
	rel, err := pathsafe.Clean(r.URL.Query().Get("path"))
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "path tidak valid")
		return
	}
	rec, err := s.store.FileByPath(user.ID, rel)
	if err != nil {
		if isNotFound(err) {
			s.jsonError(w, http.StatusNotFound, "berkas tidak ditemukan")
			return
		}
		s.jsonError(w, http.StatusInternalServerError, "gagal membaca berkas")
		return
	}

	name := pathsafe.Base(rel)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	if rec.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(rec.Size, 10))
	}

	// Berkas yang sudah tersinkron diambil dari GitHub; yang pending dari staging.
	if rec.Status == store.StatusSynced && rec.BlobSHA != "" && s.pusher != nil {
		if err := s.pusher.DownloadBlob(r.Context(), rec.BlobSHA, w); err != nil {
			s.log.Error("unduh blob", "err", err, "path", rel)
		}
		return
	}
	f, err := os.Open(filepath.Join(s.stagingDir(user.ID), filepath.FromSlash(rel)))
	if err != nil {
		s.jsonError(w, http.StatusNotFound, "berkas tidak tersedia di server")
		return
	}
	defer f.Close()
	_, _ = io.Copy(w, f)
}

// ---------------------------------------------------------------- admin

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(w, r) {
		return
	}
	fields, err := fieldMap(r)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "payload tidak valid")
		return
	}
	username := strings.TrimSpace(strings.ToLower(fields["username"]))
	password := fields["password"]
	display := strings.TrimSpace(fields["display_name"])
	role := fields["role"]
	if role != "admin" {
		role = "user"
	}

	if err := auth.ValidateUsername(username); err != nil {
		s.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		s.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidateDisplayName(display); err != nil {
		s.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal membuat hash password")
		return
	}
	u, err := s.store.CreateUser(username, hash, display, role)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			s.jsonError(w, http.StatusConflict, "username sudah dipakai")
			return
		}
		s.jsonError(w, http.StatusInternalServerError, "gagal membuat akun")
		return
	}
	s.jsonOK(w, map[string]any{"id": u.ID, "username": u.Username})
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "id tidak valid")
		return
	}
	fields, err := fieldMap(r)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "payload tidak valid")
		return
	}
	password := fields["password"]
	if err := auth.ValidatePassword(password); err != nil {
		s.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal membuat hash password")
		return
	}
	if err := s.store.UpdatePassword(id, hash); err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal mengganti password")
		return
	}
	s.jsonOK(w, map[string]any{"ok": true})
}

func (s *Server) handleAdminToggleActive(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "id tidak valid")
		return
	}
	fields, err := fieldMap(r)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "payload tidak valid")
		return
	}
	active := fields["active"] == "1"
	if err := s.store.SetActive(id, active); err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal mengubah status akun")
		return
	}
	s.jsonOK(w, map[string]any{"active": active})
}

func (s *Server) handleAdminSetRole(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "id tidak valid")
		return
	}
	fields, err := fieldMap(r)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "payload tidak valid")
		return
	}
	role := fields["role"]
	if role != "admin" && role != "user" {
		s.jsonError(w, http.StatusBadRequest, "peran tidak valid")
		return
	}
	if err := s.store.SetRole(id, role); err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal mengubah peran")
		return
	}
	s.jsonOK(w, map[string]any{"role": role})
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "id tidak valid")
		return
	}
	admin, _ := r.Context().Value(ctxUser).(store.User)
	if admin.ID == id {
		s.jsonError(w, http.StatusBadRequest, "tidak bisa menghapus akun sendiri")
		return
	}
	target, err := s.store.UserByID(id)
	if err != nil {
		s.jsonError(w, http.StatusNotFound, "akun tidak ditemukan")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal menghapus akun")
		return
	}
	// Buang sisa staging user.
	_ = os.RemoveAll(s.stagingDir(id))
	_ = s.store.LogChange(target.ID)
	s.jsonOK(w, map[string]any{"deleted": true})
}

func (s *Server) handleAdminSaveSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(w, r) {
		return
	}
	keys := map[string]int{
		"max_upload_size_mb":    1,
		"total_quota_mb":        1,
		"max_files_per_request": 1,
		"debounce_base_min":     1,
		"debounce_min_min":      1,
		"debounce_max_min":      1,
		"burst_window_min":      1,
	}
	for key, min := range keys {
		raw := r.FormValue(key)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < min {
			s.jsonError(w, http.StatusBadRequest, fmt.Sprintf("nilai %s tidak valid", key))
			return
		}
		if key == "max_upload_size_mb" && n > 100 {
			s.jsonError(w, http.StatusBadRequest, "batas ukuran berkas maksimal 100 MB")
			return
		}
		if err := s.store.SetSetting(key, raw); err != nil {
			s.jsonError(w, http.StatusInternalServerError, "gagal menyimpan pengaturan")
			return
		}
	}
	if err := s.LoadSettings(); err != nil {
		s.jsonError(w, http.StatusInternalServerError, "gagal memuat pengaturan")
		return
	}
	s.jsonOK(w, map[string]any{"ok": true})
}

func (s *Server) handleAdminForcePush(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "id tidak valid")
		return
	}
	user, err := s.store.UserByID(id)
	if err != nil {
		s.jsonError(w, http.StatusNotFound, "akun tidak ditemukan")
		return
	}
	if s.scheduler == nil {
		s.jsonError(w, http.StatusServiceUnavailable, "sinkronisasi tidak aktif")
		return
	}
	pushCtx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	err = s.scheduler.PushNow(pushCtx, user)
	if err != nil {
		s.jsonError(w, http.StatusBadGateway, "push gagal: "+err.Error())
		return
	}
	s.jsonOK(w, map[string]any{"ok": true})
}
