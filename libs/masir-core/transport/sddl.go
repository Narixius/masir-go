package transport

import (
	"errors"
	"regexp"
)

var sidRe = regexp.MustCompile(`^S-1-\d+(-\d+)+$`)

// pipeSDDL is a protected access list (no inherited entries) granting full access to
// LocalSystem (SY), the Administrators group (BA) and the user with sid, and nobody else.
func pipeSDDL(sid string) (string, error) {
	if !sidRe.MatchString(sid) {
		return "", errors.New("the daemon's user has no valid SID: " + sid)
	}
	return "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + sid + ")", nil
}
