// Package sync menjadwalkan dan mengeksekusi push berkas ke repository.
package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lendradev/tugas-submission/internal/store"
)

// ErrNoRepo dikembalikan saat konfigurasi repo belum lengkap.
var ErrNoRepo = errors.New("konfigurasi repository belum lengkap")

// FileOp adalah satu operasi berkas dalam satu batch push.
type FileOp struct {
	// Path relatif terhadap folder user, mis. "soal.pdf" atau "uts/a.pdf".
	Path string
	// Delete menandai berkas harus dihapus dari repository.
	Delete bool
	// Content isi berkas (diabaikan bila Delete).
	Content []byte
}

// Pusher mendorong sekumpulan perubahan berkas ke repository dan mengembalikan
// SHA blob per path (kosong untuk berkas yang dihapus).
//
// Implementasi tersedia untuk GitHub (Git Data API) dan Gitea (Contents API).
type Pusher interface {
	// Push menjalankan operasi berkas milik user ke folder tugas/{username}.
	// SHA blob per path relatif dikembalikan.
	Push(ctx context.Context, username string, ops []FileOp) (map[string]string, error)
	// DownloadBlob streaming isi blob ke writer.
	DownloadBlob(ctx context.Context, sha string, w io.Writer) error
	// Ready memberi tahu apakah pusher siap dipakai.
	Ready() bool
	// Describe mengembalikan keterangan repo untuk ditampilkan di UI.
	Describe() string
}

// Debounce parametrik untuk jeda push adaptif.
type Debounce struct {
	Base        time.Duration
	Min         time.Duration
	Max         time.Duration
	BurstWindow time.Duration
}

// DefaultDebounce adalah jeda bawaan: 5 menit saat normal.
func DefaultDebounce() Debounce {
	return Debounce{
		Base:        5 * time.Minute,
		Min:         1 * time.Minute,
		Max:         30 * time.Minute,
		BurstWindow: 15 * time.Minute,
	}
}

// NextDelay menghitung jeda push berikutnya berdasarkan perilaku user.
//
//	serang (banyak perubahan) -> jeda memanjang (eksponensial, dibatasi Max)
//	sepi (jarak perubahan jauh) -> jeda memendek
func (d Debounce) NextDelay(changesInWindow int, sinceLastChange time.Duration) time.Duration {
	delay := d.Base
	switch {
	case changesInWindow >= 3:
		// Setiap tambahan perubahan menggandakan jeda: 3 perubahan = 2x, 4 = 4x, dst.
		shift := changesInWindow - 2
		if shift > 10 {
			shift = 10
		}
		delay = d.Base * time.Duration(1<<uint(shift))
	case changesInWindow == 1 && sinceLastChange > 2*time.Hour:
		delay = d.Min
	}
	if delay < d.Min {
		delay = d.Min
	}
	if delay > d.Max {
		delay = d.Max
	}
	return delay
}

// Settings adalah konfigurasi yang bisa diubah admin.
type Settings struct {
	Debounce
	MaxUploadSizeMB int
	TotalQuotaMB    int
	MaxFilesPerReq  int
}

// DefaultSettings mengembalikan pengaturan bawaan.
func DefaultSettings() Settings {
	return Settings{
		Debounce:        DefaultDebounce(),
		MaxUploadSizeMB: 25,
		TotalQuotaMB:    5120,
		MaxFilesPerReq:  20,
	}
}

// Syncer menjalankan push berkala ke repository.
type Syncer struct {
	store  *store.Store
	pusher Pusher
	log    *slog.Logger
	mu     chan struct{}
}

// New membuat Syncer. pusher nil berarti sinkronisasi nonaktif.
func New(st *store.Store, pusher Pusher, log *slog.Logger) *Syncer {
	return &Syncer{
		store:  st,
		pusher: pusher,
		log:    log,
		mu:     make(chan struct{}, 1),
	}
}

// Ready memberi tahu apakah syncer bisa bekerja.
func (s *Syncer) Ready() bool {
	return s.pusher != nil && s.pusher.Ready()
}

// Describe mengembalikan keterangan repo untuk UI.
func (s *Syncer) Describe() string {
	if s.pusher == nil {
		return ""
	}
	return s.pusher.Describe()
}

// PushUser mem-push seluruh perubahan pending milik satu user ke repository.
func (s *Syncer) PushUser(ctx context.Context, userID int64, username, stagingDir string) error {
	if !s.Ready() {
		return ErrNoRepo
	}
	select {
	case s.mu <- struct{}{}:
		defer func() { <-s.mu }()
	default:
		return errors.New("sinkronisasi lain sedang berjalan")
	}

	pending, err := s.store.PendingFiles(userID)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	ops := make([]FileOp, 0, len(pending))
	for _, f := range pending {
		op := FileOp{Path: f.Path, Delete: f.Deleted}
		if !f.Deleted {
			local := filepath.Join(stagingDir, filepath.FromSlash(f.Path))
			data, err := os.ReadFile(local)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					// Berkas hilang (dibersihkan manual): hapus dari mirror.
					_ = s.store.DeleteFileRecord(userID, f.Path)
					continue
				}
				return fmt.Errorf("baca %s: %w", f.Path, err)
			}
			op.Content = data
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return nil
	}

	blobSHAs, err := s.pusher.Push(ctx, username, ops)
	if err != nil {
		return err
	}

	// Sukses: perbarui mirror lalu buang berkas lokal agar hemat storage.
	for _, f := range pending {
		if f.Deleted {
			_ = s.store.DeleteFileRecord(userID, f.Path)
			continue
		}
		local := filepath.Join(stagingDir, filepath.FromSlash(f.Path))
		if err := os.Remove(local); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("gagal hapus staging", "path", f.Path, "err", err)
		}
	}
	// Perbarui status ke synced beserta SHA blob.
	for path, sha := range blobSHAs {
		if sha == "" {
			continue
		}
		var size int64
		for _, op := range ops {
			if op.Path == path && !op.Delete {
				size = int64(len(op.Content))
			}
		}
		if err := s.store.MarkSynced(userID, path, size, sha); err != nil {
			s.log.Warn("gagal perbarui mirror", "path", path, "err", err)
		}
	}
	pruneEmptyDirs(stagingDir)
	return nil
}

func pruneEmptyDirs(root string) {
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || p == root {
			return nil
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil
		}
		if len(entries) == 0 {
			_ = os.Remove(p)
		}
		return nil
	})
}

// Scheduler menjalankan sinkronisasi berkala.
type Scheduler struct {
	syncer  *Syncer
	store   *store.Store
	log     *slog.Logger
	staging string
	tick    time.Duration
	stop    chan struct{}
	once    chan struct{}
}

// NewScheduler membuat scheduler dengan interval tick.
func NewScheduler(s *Syncer, st *store.Store, staging string, log *slog.Logger, tick time.Duration) *Scheduler {
	if tick <= 0 {
		tick = 15 * time.Second
	}
	return &Scheduler{
		syncer:  s,
		store:   st,
		log:     log,
		staging: staging,
		tick:    tick,
		stop:    make(chan struct{}),
		once:    make(chan struct{}, 1),
	}
}

// Start menjalankan loop sinkronisasi di goroutine.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(s.tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-t.C:
				s.tickOnce(ctx)
			}
		}
	}()
}

// Stop menghentikan scheduler.
func (s *Scheduler) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

// Trigger menjalankan sinkronisasi segera di luar goroutine scheduler.
func (s *Scheduler) Trigger(ctx context.Context) {
	s.tickOnce(ctx)
}

// PushNow mem-push perubahan satu user langsung (dipakai tombol admin).
func (s *Scheduler) PushNow(ctx context.Context, user store.User) error {
	err := s.syncer.PushUser(ctx, user.ID, user.Username, filepath.Join(s.staging, fmt.Sprint(user.ID)))
	if err != nil {
		_ = s.store.SetPushResult(user.ID, time.Now(), err.Error())
		return err
	}
	_ = s.store.SetPushResult(user.ID, time.Now(), "")
	_ = s.store.SetNextPush(user.ID, time.Now().Add(30*time.Minute))
	return nil
}

func (s *Scheduler) tickOnce(ctx context.Context) {
	select {
	case s.once <- struct{}{}:
	default:
		return
	}
	defer func() { <-s.once }()

	if !s.syncer.Ready() {
		return
	}
	states, err := s.store.SyncStateUsers()
	if err != nil {
		s.log.Error("gagal baca sync state", "err", err)
		return
	}
	now := time.Now()
	for _, st := range states {
		if st.NextPushAt == nil || st.NextPushAt.After(now) {
			continue
		}
		user, err := s.store.UserByID(st.UserID)
		if err != nil {
			continue
		}
		if !user.Active {
			continue
		}
		pushCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err = s.syncer.PushUser(pushCtx, user.ID, user.Username, filepath.Join(s.staging, fmt.Sprint(user.ID)))
		cancel()
		if err != nil {
			s.log.Error("push gagal", "user", user.Username, "err", err)
			_ = s.store.SetPushResult(user.ID, time.Now(), err.Error())
			// Coba lagi nanti; jangan hentikan antrean.
			_ = s.store.SetNextPush(user.ID, time.Now().Add(5*time.Minute))
			continue
		}
		_ = s.store.SetPushResult(user.ID, time.Now(), "")
		_ = s.store.SetNextPush(user.ID, time.Now().Add(30*time.Minute))
	}
}

// ScheduleNext menghitung dan menyimpan jadwal push berikutnya untuk user.
func (s *Scheduler) ScheduleNext(userID int64, d Debounce) error {
	changes, err := s.store.RecentChanges(userID, d.BurstWindow)
	if err != nil {
		return err
	}
	last, ok, err := s.store.LastChangeAt(userID)
	if err != nil {
		return err
	}
	since := time.Duration(0)
	if ok {
		since = time.Since(last)
	}
	delay := d.NextDelay(changes, since)
	return s.store.SetNextPush(userID, time.Now().Add(delay))
}

// DescribeNextPush mengembalikan waktu push berikutnya untuk user.
func DescribeNextPush(st store.SyncState) string {
	if st.NextPushAt == nil {
		return "belum dijadwalkan"
	}
	return st.NextPushAt.Format("2006-01-02 15:04")
}

// RepoPath menghitung path lengkap berkas di repository.
func RepoPath(username, rel string) string {
	return strings.Join([]string{"tugas", username, rel}, "/")
}
