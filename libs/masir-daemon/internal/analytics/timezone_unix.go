//go:build !windows

package analytics

import (
	"os"
	"strings"
	"time"
)

// LocalTimezone returns the best available IANA name, UTC offset in minutes and zone
// abbreviation. The name is empty when the system only exposes "Local".
func LocalTimezone() (name string, offsetMinutes int, abbrev string) {
	name = strings.TrimSpace(os.Getenv("TZ"))
	name = strings.TrimPrefix(name, ":")
	if !strings.Contains(name, "/") && name != "UTC" && name != "GMT" {
		name = ""
	}
	if name == "" {
		if target, err := os.Readlink("/etc/localtime"); err == nil {
			if i := strings.Index(target, "zoneinfo/"); i >= 0 {
				name = target[i+len("zoneinfo/"):]
			}
		}
	}
	abbrev, seconds := time.Now().Zone()
	return name, seconds / 60, abbrev
}
