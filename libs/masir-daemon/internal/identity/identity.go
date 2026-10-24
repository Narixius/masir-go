// Package identity works out which user masird serves and whether it runs as root.
package identity

import (
	"errors"
	"os"
	"os/user"
	"strconv"
)

type Identity struct {
	Privileged bool // running as root, so TUN is available
	UID, GID   int  // the served user; -1 where the OS has no numeric ids (Windows)
	Name       string
	Home       string
}

// Resolve returns the current user, or when running as root the user named by flagUser
// (a name or uid), falling back to SUDO_UID: masird always serves one regular user.
func Resolve(flagUser string) (Identity, error) {
	if os.Geteuid() != 0 {
		u, err := user.Current()
		if err != nil {
			return Identity{}, err
		}
		return from(u, false), nil
	}
	spec := flagUser
	if spec == "" {
		spec = os.Getenv("SUDO_UID")
	}
	if spec == "" {
		return Identity{}, errors.New("running as root: pass --user <name|uid> for the user masird serves")
	}
	u, err := user.LookupId(spec)
	if err != nil {
		if u, err = user.Lookup(spec); err != nil {
			return Identity{}, err
		}
	}
	if u.Uid == "0" {
		return Identity{}, errors.New("masird serves a regular user, not root")
	}
	return from(u, true), nil
}

func from(u *user.User, privileged bool) Identity {
	return Identity{Privileged: privileged, UID: atoi(u.Uid), GID: atoi(u.Gid), Name: u.Username, Home: u.HomeDir}
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
