// Package store mengimplementasikan akses data berbasis SQLite (pure Go, WAL).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound dikembalikan saat baris tidak ada.
var ErrNotFound = errors.New("tidak ditemukan")

// ErrDuplicate dikembalikan saat username sudah dipakai.
var ErrDuplicate = errors.New("username sudah dipakai")

// Store adalah pembungkus database.
type Store struct {
	db *sql.DB
}

// User adalah akun pengguna.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	DisplayName  string
	Role         string
	Active       bool
	CreatedAt    time.Time
}

// Session adalah sesi login.
type Session struct {
	ID        string
	UserID    int64
	CSRF      string
	ExpiresAt time.Time
}

// File adalah entri mirror index untuk satu berkas di folder user.
type File struct {
	ID        int64
	UserID    int64
	Path      string
	Size      int64
	BlobSHA   string
	Status    string
	Deleted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Status sinkronisasi berkas.
const (
	StatusPending = "pending"
	StatusSynced  = "synced"
)

// SyncState menyimpan status sinkronisasi per user.
type SyncState struct {
	UserID     int64
	LastPushAt *time.Time
	NextPushAt *time.Time
	LastError  string
	PushCount  int
}

// Open membuka (dan membuat bila perlu) database di path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close menutup koneksi database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'user',
			active INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			blob_sha TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			deleted INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(user_id, path)
		)`,
		`CREATE TABLE IF NOT EXISTS change_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_change_log_user ON change_log(user_id, at)`,
		`CREATE TABLE IF NOT EXISTS sync_state (
			user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			last_push_at TEXT,
			next_push_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			push_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- users

// CreateUser membuat akun baru. Username harus unik.
func (s *Store) CreateUser(username, passwordHash, displayName, role string) (User, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, display_name, role, active, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		username, passwordHash, displayName, role, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrDuplicate
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, PasswordHash: passwordHash, DisplayName: displayName,
		Role: role, Active: true, CreatedAt: time.Now().UTC()}, nil
}

// UserByUsername mengambil akun berdasarkan username.
func (s *Store) UserByUsername(username string) (User, error) {
	return s.scanUser(s.db.QueryRow(
		`SELECT id, username, password_hash, display_name, role, active, created_at FROM users WHERE username = ?`, username))
}

// UserByID mengambil akun berdasarkan id.
func (s *Store) UserByID(id int64) (User, error) {
	return s.scanUser(s.db.QueryRow(
		`SELECT id, username, password_hash, display_name, role, active, created_at FROM users WHERE id = ?`, id))
}

// ListUsers mengembalikan seluruh akun urut nama.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(
		`SELECT id, username, password_hash, display_name, role, active, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := s.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePassword mengganti hash password.
func (s *Store) UpdatePassword(id int64, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	return err
}

// UpdateDisplayName mengganti display name.
func (s *Store) UpdateDisplayName(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE users SET display_name = ? WHERE id = ?`, name, id)
	return err
}

// SetActive mengaktifkan atau menonaktifkan akun.
func (s *Store) SetActive(id int64, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE users SET active = ? WHERE id = ?`, v, id)
	return err
}

// SetRole mengubah peran akun.
func (s *Store) SetRole(id int64, role string) error {
	_, err := s.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, id)
	return err
}

// DeleteUser menghapus akun beserta sesi dan berkasnya (cascade).
func (s *Store) DeleteUser(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM files WHERE user_id = ?`,
		`DELETE FROM change_log WHERE user_id = ?`,
		`DELETE FROM sync_state WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var active int
	var created string
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &active, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u.Active = active == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return u, nil
}

// ---------------------------------------------------------------- sessions

// CreateSession menyimpan sesi baru.
func (s *Store) CreateSession(id string, userID int64, csrf string, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO sessions (id, user_id, csrf, expires_at) VALUES (?, ?, ?, ?)`,
		id, userID, csrf, expires.UTC().Format(time.RFC3339))
	return err
}

// Session mengambil sesi aktif; mengembalikan ErrNotFound bila habis.
func (s *Store) Session(id string) (Session, error) {
	var sess Session
	var expires string
	err := s.db.QueryRow(`SELECT id, user_id, csrf, expires_at FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.UserID, &sess.CSRF, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	t, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		return Session{}, ErrNotFound
	}
	if time.Now().After(t) {
		s.DeleteSession(id)
		return Session{}, ErrNotFound
	}
	sess.ExpiresAt = t
	return sess, nil
}

// DeleteSession menghapus sesi.
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PurgeExpiredSessions menghapus sesi kedaluwarsa.
func (s *Store) PurgeExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339))
	return err
}

// ---------------------------------------------------------------- files

// UpsertFile menulis entri mirror index.
func (s *Store) UpsertFile(userID int64, path string, size int64, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT INTO files (user_id, path, size, status, deleted, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(user_id, path) DO UPDATE SET size = excluded.size, status = excluded.status,
			deleted = 0, updated_at = excluded.updated_at`,
		userID, path, size, status, now, now)
	return err
}

// MarkSynced menandai berkas sudah ada di GitHub.
func (s *Store) MarkSynced(userID int64, path string, size int64, blobSHA string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE files SET status = ?, size = ?, blob_sha = ?, deleted = 0, updated_at = ?
		WHERE user_id = ? AND path = ?`, StatusSynced, size, blobSHA, now, userID, path)
	return err
}

// MarkDeleted menandai berkas untuk dihapus di GitHub.
func (s *Store) MarkDeleted(userID int64, path string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE files SET status = ?, deleted = 1, updated_at = ?
		WHERE user_id = ? AND path = ?`, StatusPending, now, userID, path)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// Berkas hanya ada di GitHub, belum pernah tercatat lokal.
	_, err = s.db.Exec(`INSERT INTO files (user_id, path, size, status, deleted, created_at, updated_at)
		VALUES (?, ?, 0, ?, 1, ?, ?)`, userID, path, StatusPending, now, now)
	return err
}

// PendingFiles mengembalikan berkas milik user yang menunggu push.
func (s *Store) PendingFiles(userID int64) ([]File, error) {
	return s.queryFiles(`SELECT id, user_id, path, size, blob_sha, status, deleted, created_at, updated_at
		FROM files WHERE user_id = ? AND status = ? ORDER BY path`, userID, StatusPending)
}

// AllFiles mengembalikan seluruh entri mirror milik user (tanpa yang ditandai terhapus).
func (s *Store) AllFiles(userID int64) ([]File, error) {
	return s.queryFiles(`SELECT id, user_id, path, size, blob_sha, status, deleted, created_at, updated_at
		FROM files WHERE user_id = ? AND deleted = 0 ORDER BY path`, userID)
}

// FileByPath mengambil satu entri mirror.
func (s *Store) FileByPath(userID int64, path string) (File, error) {
	rows, err := s.queryFiles(`SELECT id, user_id, path, size, blob_sha, status, deleted, created_at, updated_at
		FROM files WHERE user_id = ? AND path = ? AND deleted = 0`, userID, path)
	if err != nil {
		return File{}, err
	}
	if len(rows) == 0 {
		return File{}, ErrNotFound
	}
	return rows[0], nil
}

// DeleteFileRecord menghapus entri mirror sepenuhnya (dipakai setelah push hapus).
func (s *Store) DeleteFileRecord(userID int64, path string) error {
	_, err := s.db.Exec(`DELETE FROM files WHERE user_id = ? AND path = ?`, userID, path)
	return err
}

// UserSize menjumlahkan ukuran mirror milik user.
func (s *Store) UserSize(userID int64) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRow(`SELECT SUM(size) FROM files WHERE user_id = ? AND deleted = 0`, userID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// TotalSize menjumlahkan ukuran mirror seluruh user.
func (s *Store) TotalSize() (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRow(`SELECT SUM(size) FROM files WHERE deleted = 0`).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// CountFiles menghitung berkas per user.
func (s *Store) CountFiles(userID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE user_id = ? AND deleted = 0`, userID).Scan(&n)
	return n, err
}

func (s *Store) queryFiles(q string, args ...any) ([]File, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		var deleted int
		var created, updated string
		if err := rows.Scan(&f.ID, &f.UserID, &f.Path, &f.Size, &f.BlobSHA, &f.Status, &deleted, &created, &updated); err != nil {
			return nil, err
		}
		f.Deleted = deleted == 1
		f.CreatedAt, _ = time.Parse(time.RFC3339, created)
		f.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- change log

// LogChange mencatat aktivitas perubahan (untuk menghitung debounce adaptif).
func (s *Store) LogChange(userID int64) error {
	_, err := s.db.Exec(`INSERT INTO change_log (user_id, at) VALUES (?, ?)`,
		userID, time.Now().UTC().Format(time.RFC3339))
	return err
}

// RecentChanges menghitung perubahan dalam rentang waktu terakhir.
func (s *Store) RecentChanges(userID int64, since time.Duration) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE user_id = ? AND at >= ?`,
		userID, time.Now().UTC().Add(-since).Format(time.RFC3339)).Scan(&n)
	return n, err
}

// LastChange_at mengembalikan waktu perubahan terakhir.
func (s *Store) LastChangeAt(userID int64) (time.Time, bool, error) {
	var at string
	err := s.db.QueryRow(`SELECT MAX(at) FROM change_log WHERE user_id = ?`, userID).Scan(&at)
	if err != nil {
		return time.Time{}, false, err
	}
	if at == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return time.Time{}, false, nil
	}
	return t, true, nil
}

// PurgeChangeLogOlderThan menghapus log perubahan lama.
func (s *Store) PurgeChangeLogOlderThan(age time.Duration) error {
	_, err := s.db.Exec(`DELETE FROM change_log WHERE at < ?`,
		time.Now().UTC().Add(-age).Format(time.RFC3339))
	return err
}

// ---------------------------------------------------------------- sync state

// SyncStateUsers mengembalikan daftar user yang punya state sinkronisasi.
func (s *Store) SyncStateUsers() ([]SyncState, error) {
	rows, err := s.db.Query(`SELECT user_id, last_push_at, next_push_at, last_error, push_count FROM sync_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncState
	for rows.Next() {
		var st SyncState
		var last, next sql.NullString
		if err := rows.Scan(&st.UserID, &last, &next, &st.LastError, &st.PushCount); err != nil {
			return nil, err
		}
		if last.Valid {
			if t, err := time.Parse(time.RFC3339, last.String); err == nil {
				st.LastPushAt = &t
			}
		}
		if next.Valid {
			if t, err := time.Parse(time.RFC3339, next.String); err == nil {
				st.NextPushAt = &t
			}
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SetNextPush menjadwalkan push berikutnya.
func (s *Store) SetNextPush(userID int64, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO sync_state (user_id, next_push_at) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET next_push_at = excluded.next_push_at`,
		userID, at.UTC().Format(time.RFC3339))
	return err
}

// SetPushResult mencatat hasil push.
func (s *Store) SetPushResult(userID int64, at time.Time, errMsg string) error {
	_, err := s.db.Exec(`INSERT INTO sync_state (user_id, last_push_at, last_error, push_count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(user_id) DO UPDATE SET last_push_at = excluded.last_push_at,
			last_error = excluded.last_error, push_count = sync_state.push_count + 1`,
		userID, at.UTC().Format(time.RFC3339), errMsg)
	return err
}

// ---------------------------------------------------------------- settings

// Setting membaca nilai setting dengan fallback.
func (s *Store) Setting(key, def string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return def, nil
		}
		return def, err
	}
	return v, nil
}

// SetSetting menyimpan nilai setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AllSettings mengembalikan seluruh setting sebagai map.
func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
