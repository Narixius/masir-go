package analytics

import (
	"sync"
	"time"
)

var (
	tzOnce sync.Once
	tzName string
)

// TimezoneProps returns the local timezone. The name is cached because it doesn't
// change; the offset is read each time so a long-running daemon follows DST.
func TimezoneProps() map[string]any {
	tzOnce.Do(func() {
		tzName, _, _ = LocalTimezone()
	})
	abbrev, seconds := time.Now().Zone()
	p := map[string]any{"utc_offset_minutes": seconds / 60}
	if tzName != "" {
		p["$timezone"] = tzName
	}
	if abbrev != "" {
		p["tz_abbrev"] = abbrev
	}
	return p
}
