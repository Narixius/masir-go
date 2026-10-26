package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo"
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

// readApp reads an application's display name and icon. The system draws the icon on every
// platform; only macOS bundles carry a display name of their own.
func readApp(path string) (string, *ui.Bitmap) {
	if !mygo.App.IsReady() { // go test: there is no app to ask
		return "", nil
	}
	name := ""
	if runtime.GOOS == "darwin" && strings.HasSuffix(strings.TrimSuffix(path, "/"), ".app") {
		name = bundleName(path)
	}
	return name, fileIcon(path)
}

// bundleName is a macOS app bundle's display name, as Finder shows it.
func bundleName(path string) string {
	plist := filepath.Join(path, "Contents", "Info.plist")
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", plist).Output()
	if err != nil {
		return ""
	}
	var info struct {
		DisplayName string `json:"CFBundleDisplayName"`
		Name        string `json:"CFBundleName"`
	}
	if json.Unmarshal(out, &info) != nil {
		return ""
	}
	if info.DisplayName != "" {
		return info.DisplayName
	}
	return info.Name
}

// fileIcon draws the icon the system shows for path, as a small bitmap.
func fileIcon(path string) *ui.Bitmap {
	png, err := mygo.Shell.FileIcon(path, 96)
	if err != nil || len(png) == 0 {
		return nil
	}
	bmp, err := ui.DecodeBitmap(png)
	if err != nil {
		return nil
	}
	return bmp
}
