package analytics

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	coreanalytics "github.com/narixius/masir-go/libs/masir-core/analytics"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/userfs"
)

const (
	maxSpoolEventBytes = 16 << 10
	maxSpoolAge        = 7 * 24 * time.Hour
)

// clientEvents are the events a client may report, over RPC or through the spool. The
// spool is a directory the user can write to, so it gets no more trust than the RPC.
var clientEvents = map[string]bool{
	"self_update_finished": true,
	"cli_panic":            true,
}

// ClientEventAllowed reports whether a client may report an event of this name.
func ClientEventAllowed(name string) bool { return clientEvents[name] }

// CleanClientProps removes what a client may not set: PostHog's reserved "$" properties.
func CleanClientProps(props map[string]any) {
	for k := range props {
		if strings.HasPrefix(k, "$") {
			delete(props, k)
		}
	}
}

// DrainSpool sends events the CLI left while the daemon was unreachable. If analytics
// is off, the spool is deleted instead. Every event is checked as strictly as one
// arriving over RPC, and anything odd (too big, too old, not a regular file) is dropped.
func (c *Client) DrainSpool(ctx context.Context) {
	if !c.Enabled() {
		c.clearSpool()
		return
	}
	dir := coreanalytics.SpoolDir(c.dir)
	entries, err := userfs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ctx.Err() != nil {
			return
		}
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		if info, err := ent.Info(); err == nil && info.Mode().IsRegular() && info.Size() <= maxSpoolEventBytes {
			c.sendSpooled(path)
		}
		_ = userfs.Remove(path)
	}
}

func (c *Client) sendSpooled(path string) {
	raw, err := userfs.ReadFile(path)
	if err != nil || len(raw) > maxSpoolEventBytes {
		return
	}
	var e coreanalytics.Event
	if json.Unmarshal(raw, &e) != nil || !ClientEventAllowed(e.Event) || time.Since(e.CreatedAt) > maxSpoolAge {
		return
	}
	props := e.Properties
	if props == nil {
		props = map[string]any{}
	}
	CleanClientProps(props)
	props["source"] = "cli_spool"
	c.TrackAt(e.Event, props, e.CreatedAt)
}

func (c *Client) clearSpool() {
	_ = userfs.RemoveAll(coreanalytics.SpoolDir(c.dir))
}
