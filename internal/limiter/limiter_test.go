package limiter

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllowRespectsBurst(t *testing.T) {
	l := New(60, 3) // 1 token/detik, burst 3
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("permintaan %d ditolak, seharusnya diizinkan", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("permintaan ke-4 seharusnya ditolak (burst habis)")
	}
}

func TestAllowRefillsOverTime(t *testing.T) {
	l := New(600, 1) // 10 token/detik
	if !l.Allow("k") {
		t.Fatal("token pertama harus diizinkan")
	}
	if l.Allow("k") {
		t.Fatal("token kedua harus ditolak")
	}
	time.Sleep(150 * time.Millisecond)
	if !l.Allow("k") {
		t.Fatal("token seharusnya terisi kembali setelah menunggu")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := New(60, 1)
	if !l.Allow("a") {
		t.Fatal("kunci a harus diizinkan")
	}
	if !l.Allow("b") {
		t.Fatal("kunci b harus punya bucket sendiri dan diizinkan")
	}
}

func TestRetryAfter(t *testing.T) {
	l := New(60, 1)
	l.Allow("k")
	d := l.RetryAfter("k")
	if d <= 0 || d > 2*time.Second {
		t.Errorf("RetryAfter = %v, ingin 0 < d <= 2s", d)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.10:5555"
	if got := ClientIP(r, false); got != "192.0.2.10" {
		t.Errorf("ClientIP tanpa proxy = %q, ingin 192.0.2.10", got)
	}

	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	if got := ClientIP(r, false); got != "192.0.2.10" {
		t.Errorf("ClientIP tanpa proxy harus mengabaikan XFF, dapat %q", got)
	}
	if got := ClientIP(r, true); got != "203.0.113.5" {
		t.Errorf("ClientIP dengan proxy = %q, ingin 203.0.113.5", got)
	}
}
