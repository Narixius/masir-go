package analytics

import (
	"os"
	"path/filepath"
)

// NoticePath is the marker a client leaves once it has shown the analytics notice. The
// daemon sends nothing until it exists, so nobody is tracked before being told.
func NoticePath(configDir string) string {
	return filepath.Join(configDir, "analytics", "notice")
}

// NoticeShown reports whether a client has shown the notice for this config directory.
func NoticeShown(configDir string) bool {
	_, err := os.Stat(NoticePath(configDir))
	return err == nil
}

// MarkNoticeShown records that the notice is being shown.
func MarkNoticeShown(configDir string) error {
	marker := NoticePath(configDir)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("shown\n"), 0o600)
}
