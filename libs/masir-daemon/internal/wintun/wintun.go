// Package wintun carries the Wintun driver that Xray's TUN loads on Windows, so masir
// ships as a single executable there too. Windows loads wintun.dll only from beside
// the executable (or System32), so a privileged daemon writes it there before TUN
// starts. The DLLs are the unmodified builds from wintun.net (see LICENSE.txt, which
// allows shipping them with software that uses only the Wintun API, as Xray does);
// scripts/update-wintun.sh refreshes them.
package wintun

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// Name is the file Windows looks for.
const Name = "wintun.dll"

// ensure writes data to dir/Name unless an identical file is already there. A
// different copy may be loaded by a running Xray, and Windows can't overwrite a loaded
// DLL but can rename it, so it's moved aside first.
func ensure(dir string, data []byte) error {
	if len(data) == 0 {
		return errors.New("this build carries no Wintun driver")
	}
	path := filepath.Join(dir, Name)
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, data) {
		return nil
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		os.Remove(path + ".old")
		if err := os.Rename(path, path+".old"); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(path + ".old") // fails harmlessly while the old copy is still loaded
	return nil
}
