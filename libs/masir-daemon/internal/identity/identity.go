// Package identity works out which user the daemon serves and whether it runs with the
// privileges TUN needs (root, or an elevated Administrator on Windows).
package identity

import (
	"os/user"
	"strconv"
)

type Identity struct {
	Privileged bool // root, or an elevated Administrator: TUN is available
	UID, GID   int  // the served user; -1 where the OS has no numeric ids (Windows)
	SID        string
	Name       string
	Home       string
}

func from(u *user.User, privileged bool) Identity {
	return Identity{Privileged: privileged, UID: atoi(u.Uid), GID: atoi(u.Gid), SID: u.Uid, Name: u.Username, Home: u.HomeDir}
}

// lookup finds a user by id (uid, or SID on Windows) or by name.
func lookup(spec string) (*user.User, error) {
	u, err := user.LookupId(spec)
	if err != nil {
		return user.Lookup(spec)
	}
	return u, nil
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
