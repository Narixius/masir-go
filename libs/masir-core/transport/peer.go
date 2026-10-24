package transport

// Peer is the user allowed to connect besides root (or, on Windows, LocalSystem and
// Administrators): the user the daemon serves.
type Peer struct {
	UID, GID int    // Unix
	SID      string // Windows
}
