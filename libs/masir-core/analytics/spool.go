// Package analytics holds the small parts of analytics shared by the CLI and the
// daemon: the local event spool used when the daemon can't accept an event.
package analytics

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxSpoolEvents = 100
	maxSpoolBytes  = 256 << 10
)

// Event is one spooled client event.
type Event struct {
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties"`
	CreatedAt  time.Time      `json:"created_at"`
}

// SpoolDir is where the CLI leaves events the daemon couldn't take.
func SpoolDir(configDir string) string { return filepath.Join(configDir, "analytics", "spool") }

// WriteSpool stores one event for the daemon to drain later. It is safe to call from
// several CLI processes: each event is a separate atomically-written file.
func WriteSpool(configDir string, e Event) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	dir := SpoolDir(configDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pruneSpool(dir)
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%d-%s.json", e.CreatedAt.UnixNano(), os.Getpid(), randSuffix())
	return writeAtomic(filepath.Join(dir, name), raw)
}

// DrainSpool hands every queued event to fn and deletes its file. Files that can't be
// parsed are removed: they would never become valid.
func DrainSpool(configDir string, fn func(Event)) error {
	dir := SpoolDir(configDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var e Event
		if json.Unmarshal(raw, &e) == nil && e.Event != "" {
			fn(e)
		}
		_ = os.Remove(path)
	}
	return nil
}

// ClearSpool deletes every queued event, e.g. when analytics is turned off.
func ClearSpool(configDir string) {
	_ = os.RemoveAll(SpoolDir(configDir))
}

func pruneSpool(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name string
		size int64
	}
	var files []item
	var total int64
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		files = append(files, item{ent.Name(), info.Size()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	for len(files) > maxSpoolEvents || total > maxSpoolBytes {
		if len(files) == 0 {
			break
		}
		_ = os.Remove(filepath.Join(dir, files[0].name))
		total -= files[0].size
		files = files[1:]
	}
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func randSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
