package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// appMeta is what the panel shows for an application rule: the app's own name, and its icon
// once that has been read. It fills in off the main thread; until then the name is the file's.
type appMeta struct {
	name string
	icon *ui.Bitmap
}

// appInfos finds applications' names and icons, once per application.
type appInfos struct {
	mu    sync.Mutex
	known map[string]*appMeta
}

func newAppInfos() *appInfos { return &appInfos{known: map[string]*appMeta{}} }

// fallbackName is an application's name from its path alone: "/Applications/Spotify.app/"
// is "Spotify".
func fallbackName(path string) string {
	base := filepath.Base(strings.TrimSuffix(path, "/"))
	return strings.TrimSuffix(base, ".app")
}

// lookup returns what is known of the application at path, and has it read in the
// background the first time, calling done on the main thread when it is.
func (in *appInfos) lookup(path string, async func(func()), done func()) *appMeta {
	in.mu.Lock()
	m, ok := in.known[path]
	if !ok {
		m = &appMeta{name: fallbackName(path)}
		in.known[path] = m
	}
	in.mu.Unlock()
	if ok {
		return m
	}
	async(func() { // not under the lock: the work takes it itself
		name, icon := readApp(path)
		in.mu.Lock()
		if name != "" {
			m.name = name
		}
		m.icon = icon
		in.mu.Unlock()
		done()
	})
	return m
}

// read is an application's name and icon as they are now, safe to call while they load.
func (in *appInfos) read(m *appMeta) (string, *ui.Bitmap) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return m.name, m.icon
}

// readApp reads an application bundle's display name and icon. It only knows macOS bundles.
func readApp(path string) (string, *ui.Bitmap) {
	if runtime.GOOS != "darwin" || !strings.HasSuffix(strings.TrimSuffix(path, "/"), ".app") {
		return "", nil
	}
	plist := filepath.Join(path, "Contents", "Info.plist")
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", plist).Output()
	if err != nil {
		return "", nil
	}
	var info struct {
		DisplayName string `json:"CFBundleDisplayName"`
		Name        string `json:"CFBundleName"`
		IconFile    string `json:"CFBundleIconFile"`
		IconName    string `json:"CFBundleIconName"`
	}
	if json.Unmarshal(out, &info) != nil {
		return "", nil
	}
	name := info.DisplayName
	if name == "" {
		name = info.Name
	}
	return name, appIcon(path, info.IconFile, info.IconName)
}

// appIcon converts an application's .icns icon to a small PNG (cached on disk, keyed by the
// icon file) and loads it.
func appIcon(app, file, name string) *ui.Bitmap {
	resources := filepath.Join(app, "Contents", "Resources")
	var icns string
	for _, f := range []string{file, name, "AppIcon"} {
		if f == "" {
			continue
		}
		if !strings.HasSuffix(f, ".icns") {
			f += ".icns"
		}
		if p := filepath.Join(resources, f); fileExists(p) {
			icns = p
			break
		}
	}
	if icns == "" {
		return nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(cache, "masir", "appicons")
	if os.MkdirAll(dir, 0o755) != nil {
		return nil
	}
	sum := sha1.Sum([]byte(icns))
	png := filepath.Join(dir, hex.EncodeToString(sum[:8])+".png")
	data, err := os.ReadFile(png)
	if err != nil {
		var stderr bytes.Buffer
		cmd := exec.Command("sips", "-s", "format", "png", "-Z", "96", icns, "--out", png)
		cmd.Stderr = &stderr
		if cmd.Run() != nil {
			return nil
		}
		if data, err = os.ReadFile(png); err != nil {
			return nil
		}
	}
	bmp, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil
	}
	return bmp
}
