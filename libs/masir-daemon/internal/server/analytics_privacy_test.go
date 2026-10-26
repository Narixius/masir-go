package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// masir doesn't send these events at all.
var removedEvents = []string{`"settings_changed"`, `"rules_disabled"`, `"rules_enabled"`, `"rules_"+verb`, `"routing_mode_changed"`,
	`"inbound_added"`, `"inbound_altered"`, `"inbound_removed"`, `"outbound_added"`, `"outbound_altered"`,
	`"outbound_removed"`, `"subscription_refreshed"`, `"cli_command"`}

// masir doesn't record which server a user connects through, what kind it is, or how the
// user has configured sharing, fallback, logging or refreshing. These property names must
// not come back in any event.
func TestNoEventDescribesTheUsersServer(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range removedEvents {
			if strings.Contains(string(src), ev) {
				t.Errorf("%s sends the event %s, which was removed", f, ev)
			}
		}
		for _, key := range []string{`"active_country"`, `"active_protocol"`, `"active_network"`, `"active_security"`,
			`"active_stale"`, `"countries_available"`, `"protocols"`,
			`"fallback"`, `"allow_lan"`, `"always_on_proxy"`, `"xray_log_level"`, `"sub_interval_minutes"`} {
			if strings.Contains(string(src), key) {
				t.Errorf("%s sends %s, which describes the user's server or settings", f, key)
			}
		}
	}
}
