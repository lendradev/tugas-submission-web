// Package auth menangani hashing password, sesi, dan token CSRF.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/lendradev/tugas-submission/internal/store"
)

// ErrInvalidCredentials dikembalikan bila username/password salah.
var ErrInvalidCredentials = errors.New("kredensial tidak valid")

// ErrInactive dikembalikan bila akun dinonaktifkan.
var ErrInactive = errors.New("akun dinonaktifkan")

// UsernamePattern batas username yang aman dipakai sebagai path folder di repo.
var UsernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// bcryptCost adalah biaya hashing password.
const bcryptCost = 12

// HashPassword menghasilkan hash bcrypt.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

// CheckPassword memverifikasi password terhadap hash bcrypt.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// ValidateUsername memastikan username aman dan sesuai pola.
func ValidateUsername(u string) error {
	u = strings.TrimSpace(strings.ToLower(u))
	if !UsernamePattern.MatchString(u) {
		return errors.New("username harus 3-32 karakter: huruf kecil, angka, titik, strip, atau garis bawah")
	}
	return nil
}

// ValidateDisplayName memastikan display name wajar.
func ValidateDisplayName(n string) error {
	n = strings.TrimSpace(n)
	if n == "" {
		return errors.New("display name tidak boleh kosong")
	}
	if len([]rune(n)) > 64 {
		return errors.New("display name maksimal 64 karakter")
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return errors.New("display name tidak boleh memuat karakter kontrol")
		}
	}
	return nil
}

// ValidatePassword memastikan password cukup kuat.
func ValidatePassword(pw string) error {
	if len(pw) < 8 {
		return errors.New("password minimal 8 karakter")
	}
	if len(pw) > 256 {
		return errors.New("password terlalu panjang")
	}
	return nil
}

// Auth mengelola sesi pengguna.
type Auth struct {
	store *store.Store
	ttl   time.Duration
}

// New membuat Auth.
func New(st *store.Store, ttl time.Duration) *Auth {
	return &Auth{store: st, ttl: ttl}
}

// Session adalah hasil login yang siap dikirim ke klien.
type Session struct {
	ID        string
	CSRF      string
	User      store.User
	ExpiresAt time.Time
}

// Login memverifikasi kredensial dan membuat sesi baru.
func (a *Auth) Login(username, password string) (Session, error) {
	u, err := a.store.UserByUsername(strings.TrimSpace(strings.ToLower(username)))
	if err != nil {
		// Tetap lakukan hashing agar waktu respons tidak membocorkan keberadaan akun.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$12$"+strings.Repeat("x", 53)), []byte(password))
		return Session{}, ErrInvalidCredentials
	}
	if !CheckPassword(u.PasswordHash, password) {
		return Session{}, ErrInvalidCredentials
	}
	if !u.Active {
		return Session{}, ErrInactive
	}
	id, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	expires := time.Now().Add(a.ttl)
	if err := a.store.CreateSession(id, u.ID, csrf, expires); err != nil {
		return Session{}, err
	}
	return Session{ID: id, CSRF: csrf, User: u, ExpiresAt: expires}, nil
}

// Logout menghapus sesi.
func (a *Auth) Logout(sessionID string) error {
	return a.store.DeleteSession(sessionID)
}

// Resolve mengambil sesi aktif beserta pengguna.
func (a *Auth) Resolve(sessionID string) (Session, store.User, error) {
	if sessionID == "" {
		return Session{}, store.User{}, store.ErrNotFound
	}
	sess, err := a.store.Session(sessionID)
	if err != nil {
		return Session{}, store.User{}, err
	}
	u, err := a.store.UserByID(sess.UserID)
	if err != nil {
		return Session{}, store.User{}, err
	}
	if !u.Active {
		return Session{}, store.User{}, ErrInactive
	}
	return Session{ID: sess.ID, CSRF: sess.CSRF, User: u, ExpiresAt: sess.ExpiresAt}, u, nil
}

// CheckCSRF membandingkan token CSRF secara waktu-konstan.
func CheckCSRF(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
