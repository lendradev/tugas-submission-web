package sync

import (
	"testing"
	"time"
)

func TestNextDelayBurstonRaisesDelay(t *testing.T) {
	d := DefaultDebounce()

	// 1 perubahan, baru saja -> jeda dasar 5 menit.
	got := d.NextDelay(1, 0)
	if got != 5*time.Minute {
		t.Errorf("1 perubahan = %v, ingin 5m", got)
	}

	// 3 perubahan dalam jendela -> jeda memanjang.
	got = d.NextDelay(3, time.Minute)
	if got <= 5*time.Minute {
		t.Errorf("3 perubahan = %v, ingin > 5m", got)
	}
	if got > d.Max {
		t.Errorf("3 perubahan = %v, ingin <= %v", got, d.Max)
	}

	// 4 perubahan -> jeda lebih panjang lagi (eksponensial).
	if next := d.NextDelay(4, time.Minute); next <= got {
		t.Errorf("4 perubahan = %v, ingin > %v", next, got)
	}

	// Perubahan sangat banyak -> tertahan di cap maksimum.
	got = d.NextDelay(10, time.Minute)
	if got != d.Max {
		t.Errorf("10 perubahan = %v, ingin %v", got, d.Max)
	}
}

func TestNextDelayIdleUserGetsShortDelay(t *testing.T) {
	d := DefaultDebounce()
	// User sepi: perubahan terakhir 3 jam lalu -> jeda minimum.
	got := d.NextDelay(1, 3*time.Hour)
	if got != d.Min {
		t.Errorf("user sepi = %v, ingin %v", got, d.Min)
	}
}

func TestNextDelayNeverOutOfBounds(t *testing.T) {
	d := DefaultDebounce()
	for _, n := range []int{0, 1, 2, 5, 50, 1000} {
		got := d.NextDelay(n, time.Hour)
		if got < d.Min || got > d.Max {
			t.Errorf("NextDelay(%d) = %v di luar [%v, %v]", n, got, d.Min, d.Max)
		}
	}
}

func TestRepoPath(t *testing.T) {
	if got := RepoPath("budi", "soal.pdf"); got != "tugas/budi/soal.pdf" {
		t.Errorf("RepoPath = %q, ingin tugas/budi/soal.pdf", got)
	}
}
