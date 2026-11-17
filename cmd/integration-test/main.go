// Command integration-test menjalankan pengujian end-to-end terhadap Gitea lokal.
//
// Prasyarat: Gitea berjalan di TS_GITHUB_API dengan repo + token siap pakai.
// Dijalankan sebagai: go run ./cmd/integration-test
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

type client struct {
	base string
	http *http.Client
	csrf string
}

func newClient(base string) *client {
	jar, _ := cookiejar.New(nil)
	// Redirect tidak boleh diikuti otomatis agar status 303 bisa diperiksa.
	return &client{
		base: base,
		http: &http.Client{
			Jar:     jar,
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *client) get(path string) (int, string, http.Header) {
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		log.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (c *client) postForm(path string, form url.Values) (int, string) {
	req, _ := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		log.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (c *client) postJSON(path string, payload any) (int, string) {
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf)
	resp, err := c.http.Do(req)
	if err != nil {
		log.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (c *client) upload(path string, folder string, files map[string]string) (int, string) {
	if folder != "" {
		path += "?path=" + url.QueryEscape(folder)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			log.Fatal(err)
		}
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", c.csrf)
	resp, err := c.http.Do(req)
	if err != nil {
		log.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func mustContain(label, haystack, needle string) {
	if !strings.Contains(haystack, needle) {
		log.Fatalf("GAGAL [%s]: output tidak memuat %q\noutput: %s", label, needle, haystack)
	}
	log.Printf("OK   %s (memuat %q)", label, needle)
}

func main() {
	base := env("TS_TEST_BASE", "http://127.0.0.1:18080")
	adminUser := env("TS_TEST_ADMIN_USER", "admin")
	adminPass := env("TS_TEST_ADMIN_PASS", "admin-password-123")
	userName := env("TS_TEST_USER", "budi")
	userPass := env("TS_TEST_USER_PASS", "budi-password-123")
	displayName := env("TS_TEST_DISPLAY", "Budi Santoso")

	log.Println("== 1. Login admin ==")
	admin := newClient(base)
	code, body, _ := admin.get("/login")
	if code != 200 {
		log.Fatalf("halaman login: %d", code)
	}
	code, body = admin.postForm("/login", url.Values{
		"username": {adminUser},
		"password": {adminPass},
	})
	if code != 303 {
		log.Fatalf("login admin: %d %s", code, body)
	}
	log.Println("OK   login admin")

	log.Println("== 2. Ambil CSRF ==")
	code, body, _ = admin.get("/api/status")
	if code != 200 {
		log.Fatalf("status: %d", code)
	}
	var st struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		log.Fatalf("parse status: %v (%s)", err, body)
	}
	if st.CSRF == "" {
		log.Fatal("csrf kosong")
	}
	admin.csrf = st.CSRF
	log.Println("OK   csrf diperoleh")

	log.Println("== 3. Buat akun user ==")
	code, body = admin.postJSON("/admin/users", map[string]string{
		"username":     userName,
		"password":     userPass,
		"display_name": displayName,
		"role":         "user",
	})
	if code != 200 {
		log.Fatalf("buat user: %d %s", code, body)
	}
	log.Println("OK   akun dibuat:", userName)

	log.Println("== 4. Login user ==")
	u := newClient(base)
	code, body, _ = u.get("/login")
	if code != 200 {
		log.Fatalf("login page: %d", code)
	}
	code, body = u.postForm("/login", url.Values{
		"username": {userName},
		"password": {userPass},
	})
	if code != 303 {
		log.Fatalf("login user: %d %s", code, body)
	}
	code, body, _ = u.get("/api/status")
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		log.Fatalf("parse status user: %v (%s)", err, body)
	}
	u.csrf = st.CSRF
	log.Println("OK   login user, csrf ok")

	log.Println("== 5. Upload berkas ==")
	code, body = u.upload("/api/upload", "", map[string]string{
		"soal-uts.pdf": "isi soal UTS versi 1\n",
		"jawaban.txt":  "jawaban singularity\n",
	})
	if code != 200 {
		log.Fatalf("upload: %d %s", code, body)
	}
	mustContain("upload", body, "soal-uts.pdf")
	log.Println("OK   upload diterima")

	log.Println("== 6. Daftar berkas ==")
	code, body, _ = u.get("/api/list")
	if code != 200 {
		log.Fatalf("list: %d", code)
	}
	mustContain("list", body, "soal-uts.pdf")
	mustContain("list", body, "jawaban.txt")
	log.Println("OK   berkas terdaftar di mirror index")

	log.Println("== 7. Isolasi antar user ==")
	other := newClient(base)
	code, body, _ = other.get("/login")
	_ = code
	code, body = other.postForm("/login", url.Values{
		"username": {adminUser},
		"password": {adminPass},
	})
	if code != 303 {
		log.Fatalf("login admin kedua: %d", code)
	}
	code, body, _ = other.get("/api/list")
	if strings.Contains(body, "soal-uts.pdf") {
		log.Fatalf("GAGAL: user lain melihat berkas user_test: %s", body)
	}
	log.Println("OK   user lain tidak melihat berkas milik user_test")

	log.Println("== 8. Path traversal ditolak ==")
	code, body = u.postJSON("/api/rename", map[string]string{
		"from": "../../etc/passwd",
		"to":   "x.txt",
	})
	if code == 200 {
		log.Fatalf("GAGAL: path traversal diterima: %s", body)
	}
	log.Printf("OK   path traversal ditolak (HTTP %d)", code)

	log.Println("== 9. CSRF ditolak ==")
	noCSRF := newClient(base)
	noCSRF.get("/login")
	noCSRF.postForm("/login", url.Values{"username": {userName}, "password": {userPass}})
	code, body = noCSRF.postJSON("/api/delete", map[string]any{"paths": []string{"soal-uts.pdf"}})
	if code == 200 {
		log.Fatalf("GAGAL: POST tanpa CSRF diterima: %s", body)
	}
	log.Printf("OK   POST tanpa CSRF ditolak (HTTP %d)", code)

	log.Println("== 10. Force push ke repository ==")
	code, body = admin.postForm("/admin/users/2/push", url.Values{})
	if code != 200 {
		log.Fatalf("push: %d %s", code, body)
	}
	log.Println("OK   push diterima")

	// Tunggu sinkronisasi selesai: status berubah dari pending ke synced.
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		code, body, _ = u.get("/api/list")
		if !strings.Contains(body, `"status":"pending"`) {
			break
		}
	}
	code, body, _ = u.get("/api/list")
	if strings.Contains(body, `"status":"pending"`) {
		log.Fatalf("GAGAL: berkas masih pending setelah push: %s", body)
	}
	log.Println("OK   berkas tersinkron (status synced)")

	log.Println("== 11. Unduh berkas ==")
	code, body, _ = u.get("/download?path=soal-uts.pdf")
	if code != 200 {
		log.Fatalf("download: %d %s", code, body)
	}
	mustContain("download", body, "isi soal UTS versi 1")
	log.Println("OK   unduhan berisi isi yang benar")

	log.Println("== 12. Rename ==")
	code, body = u.postJSON("/api/rename", map[string]string{
		"from": "soal-uts.pdf",
		"to":   "soal-uts-revisi.pdf",
	})
	if code != 200 {
		log.Fatalf("rename: %d %s", code, body)
	}
	code, body, _ = u.get("/api/list")
	mustContain("rename", body, "soal-uts-revisi.pdf")
	log.Println("OK   rename berhasil")

	log.Println("== 13. Hapus ==")
	code, body = u.postJSON("/api/delete", map[string]any{"paths": []string{"jawaban.txt"}})
	if code != 200 {
		log.Fatalf("delete: %d %s", code, body)
	}
	code, body, _ = u.get("/api/list")
	if strings.Contains(body, "jawaban.txt") {
		log.Fatalf("GAGAL: berkas terhapus masih muncul: %s", body)
	}
	log.Println("OK   hapus berhasil")

	log.Println()
	log.Println("SEMUA PEMERIKSAAN LULUS")
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

var _ = context.Background
