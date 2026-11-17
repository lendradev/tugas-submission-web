package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUserLifecycle(t *testing.T) {
	st := newTestStore(t)

	u, err := st.CreateUser("budi", "hash", "Budi Santoso", "user")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == 0 || u.Username != "budi" {
		t.Errorf("user tidak sesuai: %+v", u)
	}

	if _, err := st.CreateUser("budi", "hash2", "Duplikat", "user"); err != ErrDuplicate {
		t.Errorf("username ganda harus ErrDuplicate, dapat %v", err)
	}

	got, err := st.UserByUsername("budi")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}
	if got.DisplayName != "Budi Santoso" {
		t.Errorf("DisplayName = %q", got.DisplayName)
	}

	if err := st.SetActive(got.ID, false); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	got, _ = st.UserByID(got.ID)
	if got.Active {
		t.Error("akun seharusnya nonaktif")
	}

	if err := st.DeleteUser(got.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := st.UserByUsername("budi"); err != ErrNotFound {
		t.Errorf("user seharusnya terhapus, dapat %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	st := newTestStore(t)
	u, _ := st.CreateUser("siti", "hash", "Siti", "user")

	if err := st.CreateSession("sid", u.ID, "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := st.Session("sid")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if sess.CSRF != "csrf" || sess.UserID != u.ID {
		t.Errorf("sesi tidak sesuai: %+v", sess)
	}

	// Sesi kedaluwarsa harus dianggap tidak ada.
	if err := st.CreateSession("old", u.ID, "csrf", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreateSession expired: %v", err)
	}
	if _, err := st.Session("old"); err != ErrNotFound {
		t.Errorf("sesi kedaluwarsa harus ErrNotFound, dapat %v", err)
	}
}

func TestFileMirrorUpsert(t *testing.T) {
	st := newTestStore(t)
	u, _ := st.CreateUser("rina", "hash", "Rina", "user")

	if err := st.UpsertFile(u.ID, "soal.pdf", 100, StatusPending); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := st.UpsertFile(u.ID, "soal.pdf", 150, StatusPending); err != nil {
		t.Fatalf("UpsertFile kedua: %v", err)
	}

	pending, err := st.PendingFiles(u.ID)
	if err != nil {
		t.Fatalf("PendingFiles: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("jumlah pending = %d, ingin 1", len(pending))
	}
	if pending[0].Size != 150 {
		t.Errorf("size = %d, ingin 150", pending[0].Size)
	}

	if err := st.MarkSynced(u.ID, "soal.pdf", 150, "sha123"); err != nil {
		t.Fatalf("MarkSynced: %v", err)
	}
	pending, _ = st.PendingFiles(u.ID)
	if len(pending) != 0 {
		t.Errorf("pending setelah sinkron = %d, ingin 0", len(pending))
	}

	f, err := st.FileByPath(u.ID, "soal.pdf")
	if err != nil {
		t.Fatalf("FileByPath: %v", err)
	}
	if f.Status != StatusSynced || f.BlobSHA != "sha123" {
		t.Errorf("berkas tidak sesuai: %+v", f)
	}

	size, _ := st.UserSize(u.ID)
	if size != 150 {
		t.Errorf("UserSize = %d, ingin 150", size)
	}
}

func TestMarkDeletedInsertsWhenMissing(t *testing.T) {
	st := newTestStore(t)
	u, _ := st.CreateUser("andi", "hash", "Andi", "user")

	if err := st.MarkDeleted(u.ID, "hanya-di-github.pdf"); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}
	pending, _ := st.PendingFiles(u.ID)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, ingin 1", len(pending))
	}
	if !pending[0].Deleted {
		t.Error("berkas seharusnya ditandai terhapus")
	}
}

func TestChangeLogAndSettings(t *testing.T) {
	st := newTestStore(t)
	u, _ := st.CreateUser("dewi", "hash", "Dewi", "user")

	for i := 0; i < 3; i++ {
		if err := st.LogChange(u.ID); err != nil {
			t.Fatalf("LogChange: %v", err)
		}
	}
	n, err := st.RecentChanges(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("RecentChanges: %v", err)
	}
	if n != 3 {
		t.Errorf("RecentChanges = %d, ingin 3", n)
	}
	if _, ok, _ := st.LastChangeAt(u.ID); !ok {
		t.Error("LastChangeAt harus mengembalikan waktu")
	}

	if err := st.SetSetting("max_upload_size_mb", "42"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, err := st.Setting("max_upload_size_mb", "25")
	if err != nil {
		t.Fatalf("Setting: %v", err)
	}
	if v != "42" {
		t.Errorf("Setting = %q, ingin 42", v)
	}
	if v, _ := st.Setting("tidak_ada", "default"); v != "default" {
		t.Errorf("Setting fallback = %q, ingin default", v)
	}
}

func TestSyncState(t *testing.T) {
	st := newTestStore(t)
	u, _ := st.CreateUser("fajar", "hash", "Fajar", "user")

	now := time.Now()
	if err := st.SetNextPush(u.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("SetNextPush: %v", err)
	}
	if err := st.SetPushResult(u.ID, now, ""); err != nil {
		t.Fatalf("SetPushResult: %v", err)
	}
	states, err := st.SyncStateUsers()
	if err != nil {
		t.Fatalf("SyncStateUsers: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("jumlah state = %d, ingin 1", len(states))
	}
	if states[0].NextPushAt == nil || states[0].LastPushAt == nil {
		t.Errorf("state tidak lengkap: %+v", states[0])
	}
	if states[0].PushCount != 1 {
		t.Errorf("PushCount = %d, ingin 1", states[0].PushCount)
	}
}
