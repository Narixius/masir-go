package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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

// appInfos finds applications' names and icons, once per application. Icons are read one
// at a time, off the frames that open a list: drawing one through AppKit is slow, and a
// burst of them was a visible hitch when the rules first opened.
type appInfos struct {
	mu      sync.Mutex
	known   map[string]*appMeta
	queue   []string
	queued  map[string]bool
	loading bool
}

func newAppInfos() *appInfos {
	return &appInfos{known: map[string]*appMeta{}, queued: map[string]bool{}}
}

// lookup returns what is known of the application at path, and queues it to be read the
// first time. done is called on the main thread after each icon is read.
func (in *appInfos) lookup(path string, async func(func()), done func()) *appMeta {
	in.mu.Lock()
	m, ok := in.known[path]
	if !ok {
		m = &appMeta{name: fallbackName(path)}
		in.known[path] = m
	}
	if !ok && !in.queued[path] {
		in.queued[path] = true
		in.queue = append(in.queue, path)
	}
	start := !in.loading && len(in.queue) > 0
	if start {
		in.loading = true
	}
	in.mu.Unlock()
	if start {
		in.work(async, done)
	}
	return m
}

// work reads the queued applications one by one, in the background.
func (in *appInfos) work(async func(func()), done func()) {
	async(func() {
		for {
			in.mu.Lock()
			if len(in.queue) == 0 {
				in.loading = false
				in.mu.Unlock()
				return
			}
			path := in.queue[0]
			in.queue = in.queue[1:]
			delete(in.queued, path)
			in.mu.Unlock()

			name, icon := readApp(path)

			in.mu.Lock()
			if m := in.known[path]; m != nil {
				if name != "" {
					m.name = name
				}
				m.icon = icon
			}
			in.mu.Unlock()
			if done != nil {
				done()
			}
		}
	})
}

// read is an application's name and icon as they are now, safe to call while they load.
func (in *appInfos) read(m *appMeta) (string, *ui.Bitmap) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return m.name, m.icon
}

// fallbackName is an application's name from its path alone: "/Applications/Spotify.app/"
// is "Spotify", and "C:\...\Spotify.exe" is "Spotify".
func fallbackName(path string) string {
	base := filepath.Base(strings.TrimSuffix(path, "/"))
	base = strings.TrimSuffix(base, ".app")
	base = strings.TrimSuffix(base, ".exe")
	return base
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

// fileIcon draws the icon the system shows for path, as a small bitmap, kept in the cache
// for the next launch: drawing one takes AppKit a while, reading its PNG does not.
func fileIcon(path string) *ui.Bitmap {
	cache := fileIconCachePath(path)
	if cache != "" {
		if data, err := os.ReadFile(cache); err == nil {
			if bmp, err := ui.DecodeBitmap(data); err == nil {
				return bmp
			}
		}
	}
	png, err := mygo.Shell.FileIcon(path, 96)
	if err != nil || len(png) == 0 {
		return nil
	}
	bmp, err := ui.DecodeBitmap(png)
	if err != nil {
		return nil
	}
	if cache != "" {
		if os.MkdirAll(filepath.Dir(cache), 0o755) == nil {
			_ = os.WriteFile(cache, png, 0o644)
		}
	}
	return bmp
}

// fileIconCachePath names the cached icon of path, by its path and modification time, so an
// updated application gets a new one.
func fileIconCachePath(path string) string {
	dir, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		return ""
	}
	var mtime int64
	if fi, err := os.Stat(path); err == nil {
		mtime = fi.ModTime().UnixNano()
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%d", path, mtime)))
	return filepath.Join(dir, "appicons", hex.EncodeToString(sum[:8])+".png")
}
