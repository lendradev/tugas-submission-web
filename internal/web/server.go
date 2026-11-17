// Package web berisi HTTP handler, template, dan aset statis.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lendradev/tugas-submission/internal/auth"
	"github.com/lendradev/tugas-submission/internal/config"
	"github.com/lendradev/tugas-submission/internal/limiter"
	"github.com/lendradev/tugas-submission/internal/store"
	syncer "github.com/lendradev/tugas-submission/internal/sync"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Server adalah aplikasi web.
type Server struct {
	cfg       config.Config
	store     *store.Store
	auth      *auth.Auth
	pusher    syncer.Pusher
	scheduler *syncer.Scheduler
	log       *slog.Logger
	tmpl      *template.Template
	static    fs.FS

	limitGlobal *limiter.Limiter
	limitLogin  *limiter.Limiter
	limitUpload *limiter.Limiter
	uploadSem   chan struct{}

	settingsMu sync.RWMutex
	settings   syncer.Settings
}

// New membuat server siap jalan.
func New(cfg config.Config, st *store.Store, a *auth.Auth, pusher syncer.Pusher, sch *syncer.Scheduler, log *slog.Logger) (*Server, error) {
	funcs := template.FuncMap{
		"jsonify": func(v any) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("{}")
			}
			// json.Marshal sudah menghasilkan teks aman untuk atribut HTML.
			return template.JS(b)
		},
	}
	tmpl, err := template.New("").Funcs(funcs).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:         cfg,
		store:       st,
		auth:        a,
		pusher:      pusher,
		scheduler:   sch,
		log:         log,
		tmpl:        tmpl,
		static:      sub,
		limitGlobal: limiter.New(120, 60),
		limitLogin:  limiter.New(10, 5),
		limitUpload: limiter.New(30, 10),
		uploadSem:   make(chan struct{}, 4),
		settings:    syncer.DefaultSettings(),
	}
	return s, nil
}

// LoadSettings memuat pengaturan dinamis dari database.
func (s *Server) LoadSettings() error {
	all, err := s.store.AllSettings()
	if err != nil {
		return err
	}
	set := syncer.DefaultSettings()
	if v, ok := all["debounce_base_min"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			set.Debounce.Base = time.Duration(n) * time.Minute
		}
	}
	if v, ok := all["debounce_min_min"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			set.Debounce.Min = time.Duration(n) * time.Minute
		}
	}
	if v, ok := all["debounce_max_min"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			set.Debounce.Max = time.Duration(n) * time.Minute
		}
	}
	if v, ok := all["burst_window_min"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			set.Debounce.BurstWindow = time.Duration(n) * time.Minute
		}
	}
	if v, ok := all["max_upload_size_mb"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			set.MaxUploadSizeMB = n
		}
	}
	if v, ok := all["total_quota_mb"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			set.TotalQuotaMB = n
		}
	}
	if v, ok := all["max_files_per_request"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			set.MaxFilesPerReq = n
		}
	}
	s.settingsMu.Lock()
	s.settings = set
	s.settingsMu.Unlock()
	return nil
}

// Settings mengembalikan salinan pengaturan saat ini.
func (s *Server) Settings() syncer.Settings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.settings
}

// repoDesc mengembalikan keterangan repository untuk ditampilkan di UI.
func (s *Server) repoDesc() string {
	if s.pusher == nil {
		return ""
	}
	return s.pusher.Describe()
}

// Handler mengembalikan router aplikasi.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.Handle("GET /static/", http.StripPrefix("/static/", s.staticHandler()))

	mux.Handle("GET /", s.requireAuth(s.handleFilesPage))
	mux.Handle("GET /admin", s.requireAdmin(s.handleAdminPage))

	mux.Handle("GET /api/list", s.requireAuth(s.handleList))
	mux.Handle("GET /api/status", s.requireAuth(s.handleStatus))
	mux.Handle("GET /api/settings", s.requireAuth(s.handleUserSettings))
	mux.Handle("POST /api/upload", s.requireAuth(s.handleUpload))
	mux.Handle("POST /api/rename", s.requireAuth(s.handleRename))
	mux.Handle("POST /api/delete", s.requireAuth(s.handleDelete))
	mux.Handle("GET /download", s.requireAuth(s.handleDownload))

	mux.Handle("POST /admin/users", s.requireAdmin(s.handleAdminCreateUser))
	mux.Handle("GET /admin/users", s.requireAdmin(s.handleAdminListUser))
	mux.Handle("GET /admin/settings", s.requireAdmin(s.handleAdminGetSettings))
	mux.Handle("POST /admin/users/{id}/password", s.requireAdmin(s.handleAdminResetPassword))
	mux.Handle("POST /admin/users/{id}/active", s.requireAdmin(s.handleAdminToggleActive))
	mux.Handle("POST /admin/users/{id}/role", s.requireAdmin(s.handleAdminSetRole))
	mux.Handle("POST /admin/users/{id}/delete", s.requireAdmin(s.handleAdminDeleteUser))
	mux.Handle("POST /admin/settings", s.requireAdmin(s.handleAdminSaveSettings))
	mux.Handle("POST /admin/users/{id}/push", s.requireAdmin(s.handleAdminForcePush))

	return s.securityHeaders(s.recoverer(mux))
}

func (s *Server) staticHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path.Base(r.URL.Path) {
		case "alpine.min.js":
			// Versi Alpine dikunci dan jarang berubah -> cache panjang.
			w.Header().Set("Cache-Control", "public, max-age=86400")
		case "app.js", "app.css":
			// Berkas aplikasi berubah sering -> harus selalu divalidasi ulang.
			w.Header().Set("Cache-Control", "no-cache")
		default:
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// securityHeaders menambahkan header keamanan pada setiap respons.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'")
		if s.cfg.TLSCert != "" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec, "path", r.URL.Path)
				http.Error(w, "terjadi kesalahan pada server", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- middleware

type ctxKey string

const (
	ctxUser    ctxKey = "user"
	ctxCSRF    ctxKey = "csrf"
	ctxSession ctxKey = "session"
)

// requireAuth mewajibkan sesi valid.
func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := limiter.ClientIP(r, s.cfg.TrustedProxy)
		if !s.limitGlobal.Allow(ip) {
			s.tooMany(w, r, s.limitGlobal, ip)
			return
		}
		sess, user, err := s.auth.Resolve(s.cookieSession(r))
		if err != nil {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			s.jsonError(w, http.StatusUnauthorized, "sesi berakhir, silakan masuk kembali")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxCSRF, sess.CSRF)
		ctx = context.WithValue(ctx, ctxSession, sess)
		next(w, r.WithContext(ctx))
	})
}

// requireAdmin mewajibkan sesi valid dengan peran admin.
//
// Rute admin tidak memakai requireAuth terpisah supaya sesi tetap di-resolve
// tepat satu kali.
func (s *Server) requireAdmin(next http.HandlerFunc) http.Handler {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		user, _ := r.Context().Value(ctxUser).(store.User)
		if user.Role != "admin" {
			if strings.HasPrefix(r.URL.Path, "/api/") || r.Method == http.MethodPost {
				s.jsonError(w, http.StatusForbidden, "akses khusus admin")
				return
			}
			http.Error(w, "akses khusus admin", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// requireCSRF memeriksa token CSRF untuk permintaan yang mengubah data.
func (s *Server) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	want, _ := r.Context().Value(ctxCSRF).(string)
	if !auth.CheckCSRF(r.Header.Get("X-CSRF-Token"), want) {
		s.jsonError(w, http.StatusForbidden, "token CSRF tidak valid")
		return false
	}
	return true
}

// ---------------------------------------------------------------- helpers

func (s *Server) cookieSession(r *http.Request) string {
	c, err := r.Cookie("ts_session")
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     "ts_session",
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.TLSCert != "",
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "ts_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.TLSCert != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func (s *Server) jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) tooMany(w http.ResponseWriter, r *http.Request, l *limiter.Limiter, key string) {
	retry := int(l.RetryAfter(key).Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	if strings.HasPrefix(r.URL.Path, "/api/") || r.Method == http.MethodPost {
		s.jsonError(w, http.StatusTooManyRequests, fmt.Sprintf("terlalu banyak permintaan, coba lagi dalam %d detik", retry))
		return
	}
	http.Error(w, "terlalu banyak permintaan", http.StatusTooManyRequests)
}

func (s *Server) stagingDir(userID int64) string {
	return filepath.Join(s.cfg.StagingDir(), strconv.FormatInt(userID, 10))
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render template", "name", name, "err", err)
	}
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// fieldMap membaca parameter dari body baik JSON, form-encoded, maupun
// multipart, supaya endpoint yang sama bisa dipakai dari browser dan dari
// klien API.
func fieldMap(r *http.Request) (map[string]string, error) {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		var raw map[string]string
		if err := decodeJSON(r, &raw); err != nil {
			return nil, err
		}
		return raw, nil
	case strings.HasPrefix(ct, "multipart/form-data"):
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		out := make(map[string]string, len(r.MultipartForm.Value))
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				out[k] = v[0]
			}
		}
		return out, nil
	default:
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		out := make(map[string]string, len(r.PostForm))
		for k, v := range r.PostForm {
			if len(v) > 0 {
				out[k] = v[0]
			}
		}
		return out, nil
	}
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

var errUnsupported = errors.New("tidak didukung")

// ensureDir membuat folder bila belum ada.
func ensureDir(path string) error { return os.MkdirAll(path, 0o750) }
