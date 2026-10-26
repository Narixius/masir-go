package daemon

import (
	"io"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/rulecheck"
)

// CheckRules is `masir daemon check`: a running daemon asks a newly downloaded masir,
// over stdin, whether it can build the user's rules with the geo data on disk, before
// that version is installed. It writes the rules it can't, as JSON, and changes nothing.
func CheckRules(in io.Reader, out io.Writer) error { return rulecheck.Dry(in, out) }
