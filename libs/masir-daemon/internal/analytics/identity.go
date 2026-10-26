package analytics

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/userfs"
)

const identityFile = "analytics.json"

type identity struct {
	InstallID string    `json:"installId"`
	CreatedAt time.Time `json:"createdAt"`
}

// installID reads or creates the anonymous install ID. It is random and not derived
// from the machine, user or account.
func installID(dir string) (string, error) {
	path := filepath.Join(dir, identityFile)
	raw, err := userfs.ReadFile(path)
	if err == nil {
		var id identity
		if json.Unmarshal(raw, &id) == nil && id.InstallID != "" {
			return id.InstallID, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	id := identity{InstallID: newID(), CreatedAt: time.Now().UTC()}
	raw, _ = json.MarshalIndent(id, "", "  ")
	if err := userfs.Write(path, raw); err != nil {
		return "", err
	}
	return id.InstallID, nil
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
