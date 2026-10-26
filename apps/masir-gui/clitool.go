package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// cliStatus is what Settings shows of the masir command-line tool: whether the app
// installed it, and whether a shell finds it.
type cliStatus struct {
	installed bool
	onPath    bool
	path      string // the command itself
	dir       string // where it is, for the PATH hint
}

// samePath reports whether a and b name the same file, resolving links. Windows paths are
// case-insensitive.
func samePath(a, b string) bool {
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if p, err := filepath.Abs(p); err == nil {
			return p
		}
		return filepath.Clean(p)
	}
	a, b = abs(a), abs(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// homeDir is the user's home, which the unix install writes under.
func homeDir() (string, error) { return userHome() }

// userHome is swapped by tests with a temporary home.
var userHome = os.UserHomeDir
