// Package limiter menyediakan rate limiting berbasis token bucket.
package limiter

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter membatasi laju permintaan per kunci (IP, username, dll).
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // token per detik
	burst   float64
	lastGC  time.Time
	gcEvery time.Duration
}

// bucket menyimpan state token bucket untuk satu kunci.
type bucket struct {
	tokens float64
	last   time.Time
}

// New membuat limiter dengan rate (token/detik) dan burst maksimum.
func New(ratePerMinute, burst int) *Limiter {
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    float64(ratePerMinute) / 60.0,
		burst:   float64(burst),
		lastGC:  time.Now(),
		gcEvery: 5 * time.Minute,
	}
}

// Allow mencoba memakai satu token untuk kunci.
func (l *Limiter) Allow(key string) bool {
	return l.AllowN(key, 1)
}

// AllowN mencoba memakai n token untuk kunci.
func (l *Limiter) AllowN(key string, n float64) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.gc(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// RetryAfter memberi perkiraan detik sampai kunci boleh lagi.
func (l *Limiter) RetryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		return 0
	}
	if b.tokens >= 1 {
		return 0
	}
	missing := 1 - b.tokens
	if l.rate <= 0 {
		return time.Minute
	}
	return time.Duration(missing / l.rate * float64(time.Second))
}

// Reset menghapus state untuk kunci (dipakai setelah login berhasil).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

func (l *Limiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < l.gcEvery {
		return
	}
	l.lastGC = now
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
}

// ClientIP mengambil alamat IP klien, menghormati X-Forwarded-For hanya bila
// server dikonfigurasi BehindProxy.
func ClientIP(r *http.Request, behindProxy bool) string {
	if behindProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := splitComma(xff)
			if len(parts) > 0 {
				return strings.TrimSpace(parts[0])
			}
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
