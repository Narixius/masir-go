package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir is the current user's masir directory, created if missing.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir() // ~/Library/Application Support on macOS
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "masir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// ConfigDirOf is where ConfigDir points for the user whose home is home, so a root
// daemon and that user's CLI agree. On Linux it assumes the default XDG location.
func ConfigDirOf(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "masir")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "masir")
	default:
		return filepath.Join(home, ".config", "masir")
	}
}
