//go:build !windows

package identity

import (
	"errors"
	"os"
	"os/user"
)

// Resolve returns the current user, or when running as root the user named by flagUser
// (a name or uid), falling back to SUDO_UID: the daemon always serves one regular user.
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
		return Identity{}, errors.New("running as root: pass --user <name|uid> for the user the daemon serves")
	}
	u, err := lookup(spec)
	if err != nil {
		return Identity{}, err
	}
	if u.Uid == "0" {
		return Identity{}, errors.New("the daemon serves a regular user, not root")
	}
	return from(u, true), nil
}
