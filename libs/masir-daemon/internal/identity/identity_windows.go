package identity

import (
	"errors"
	"os/user"

	"golang.org/x/sys/windows"
)

// localSystem is the SID of the account Windows services run as.
const localSystem = "S-1-5-18"

// Resolve returns the user the daemon serves. Elevated (an Administrator, or the
// service running as LocalSystem), it can create the TUN adapter. The service names
// its user with flagUser (a SID or name); an elevated daemon started by hand without
// one serves whoever started it, since UAC elevation keeps the same account.
func Resolve(flagUser string) (Identity, error) {
	elevated := windows.GetCurrentProcessToken().IsElevated()
	if flagUser != "" && elevated {
		u, err := lookup(flagUser)
		if err != nil {
			return Identity{}, err
		}
		if u.Uid == localSystem {
			return Identity{}, errors.New("the daemon serves a regular user, not LocalSystem")
		}
		return from(u, true), nil
	}
	u, err := user.Current()
	if err != nil {
		return Identity{}, err
	}
	if u.Uid == localSystem {
		return Identity{}, errors.New("running as LocalSystem: pass --user <SID|name> for the user the daemon serves")
	}
	return from(u, elevated), nil
}
