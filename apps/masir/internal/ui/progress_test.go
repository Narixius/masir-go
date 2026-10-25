package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func TestProgressBar(t *testing.T) {
	for _, tc := range []struct {
		done, total int64
		filled      int
	}{
		{0, 100, 0},
		{50, 100, 5},
		{100, 100, 10},
		{120, 100, 10}, // clamped
	} {
		got := plain(progressBar(tc.done, tc.total, 10, 0))
		if n := strings.Count(got, progressFill); n != tc.filled {
			t.Errorf("progressBar(%d,%d) has %d filled cells, want %d: %q", tc.done, tc.total, n, tc.filled, got)
		}
		if n := len([]rune(got)); n != 10 {
			t.Errorf("progressBar(%d,%d) width = %d, want 10: %q", tc.done, tc.total, n, got)
		}
	}
	// No total still draws a full line, with a moving segment.
	got := plain(progressBar(0, 0, 12, 3))
	if n := len([]rune(got)); n != 12 || !strings.Contains(got, progressFill) {
		t.Errorf("indeterminate bar = %q", got)
	}
	// A tiny width must not panic.
	_ = plain(progressBar(0, 0, 1, 0))
}

func TestProgressStats(t *testing.T) {
	got := plain(progressStats(5<<20, 10<<20, 2.5*1024*1024, 2*time.Second))
	for _, want := range []string{"50%", "5.0 MB", "10.0 MB", "2.5 MB/s", "ETA 2s"} {
		if !strings.Contains(got, want) {
			t.Errorf("progressStats = %q; missing %q", got, want)
		}
	}
	if got := plain(progressStats(0, 0, 0, 0)); got != "0 B" {
		t.Errorf("unknown-total stats = %q", got)
	}
}

func TestBytesAndDuration(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1024: "1.00 KB", 5 << 20: "5.00 MB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
	for d, want := range map[time.Duration]string{
		4 * time.Second:  "4s",
		90 * time.Second: "1m 30s",
		2 * time.Hour:    "2h 0m",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
