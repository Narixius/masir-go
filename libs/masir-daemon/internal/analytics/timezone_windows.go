//go:build windows

package analytics

import (
	"time"

	"golang.org/x/sys/windows/registry"
)

// LocalTimezone returns the Windows time zone name plus the current offset and
// abbreviation. The name is Windows' "Iran Standard Time" style rather than IANA, but
// it still groups users by region; the offset is always available.
func LocalTimezone() (name string, offsetMinutes int, abbrev string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, registry.QUERY_VALUE)
	if err == nil {
		name, _, _ = k.GetStringValue("TimeZoneKeyName")
		k.Close()
	}
	abbrev, seconds := time.Now().Zone()
	return name, seconds / 60, abbrev
}
