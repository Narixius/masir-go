//go:build linux

package main

import (
	"strings"
	"testing"
)

// The daemon ends its streams and exits on SIGTERM; the unit only needs a bound for a stop
// that can't finish, so `systemctl stop`/`restart` never waits systemd's 90s default.
func TestServiceUnitBoundsTheStop(t *testing.T) {
	u := serviceUnit("/usr/local/lib/masir/masir", 1000)
	for _, want := range []string{
		"ExecStart=/usr/local/lib/masir/masir daemon run --user 1000",
		"TimeoutStopSec=10",
		"Restart=on-failure",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("unit is missing %q:\n%s", want, u)
		}
	}
}
