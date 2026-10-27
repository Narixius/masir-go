//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The command-line tool on Windows is the app's own bin directory on the user's PATH:
// nothing to copy, so an update of the app carries the command with it. HKCU needs no
// administrator.

const userEnvKey = `Environment`

const machineEnvKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`

func cliCommand() string { return "masir.exe" }

func cliStatusNow(bin string) cliStatus {
	st := cliStatus{}
	found, ok := findCLI()
	if !ok {
		return st
	}
	st.installed = true
	st.path = found
	st.dir = filepath.Dir(found)
	st.ours = samePath(found, filepath.Join(filepath.Dir(bin), cliCommand()))
	st.onPath = windowsOnPath(st.dir)
	return st
}

// findCLI looks for any masir the user's PATH reaches. It does not count the copy beside
// the app until its directory is actually on the PATH.
func findCLI() (string, bool) {
	seen := map[string]bool{}
	try := func(list string) (string, bool) {
		for _, dir := range pathDirs(list) {
			key := strings.ToLower(dir)
			if seen[key] {
				continue
			}
			seen[key] = true
			p := filepath.Join(dir, cliCommand())
			if fileExists(p) {
				return p, true
			}
		}
		return "", false
	}
	if p, ok := try(os.Getenv("PATH")); ok {
		return p, true
	}
	if p, ok := try(userPath()); ok {
		return p, true
	}
	if p, ok := try(machinePath()); ok {
		return p, true
	}
	return "", false
}

func windowsOnPath(dir string) bool {
	return pathHasDir(os.Getenv("PATH"), dir) ||
		pathHasDir(userPath(), dir) ||
		pathHasDir(machinePath(), dir)
}

// installCLI puts the app's bin directory on the user's PATH. It changes nothing
// machine-wide, so it asks for no permission. A masir that is already on the PATH is
// left where it is.
func installCLI(bin string) (cliStatus, error) {
	if bin == "" || !fileExists(bin) {
		return cliStatus{}, errors.New("the app ships no masir executable to install")
	}
	own := filepath.Join(filepath.Dir(bin), cliCommand())
	if p, ok := findCLI(); ok && !samePath(p, own) {
		return cliStatusNow(bin), nil
	}
	if err := addUserPath(filepath.Dir(bin)); err != nil {
		return cliStatus{}, err
	}
	return cliStatusNow(bin), nil
}

func removeCLI(bin string) error {
	if bin == "" {
		return errors.New("the app ships no masir executable to remove")
	}
	return removeUserPath(filepath.Dir(bin))
}

func repairCLI(bin string) {
	if bin != "" {
		_, _ = installCLI(bin)
	}
}

func userPath() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, userEnvKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err != nil {
		return ""
	}
	return v
}

func machinePath() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, machineEnvKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		k, err = registry.OpenKey(registry.LOCAL_MACHINE, machineEnvKey, registry.QUERY_VALUE)
		if err != nil {
			return ""
		}
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err != nil {
		return ""
	}
	return v
}

func setUserPath(v string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, userEnvKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	_, typ, _ := k.GetStringValue("Path")
	if typ == registry.EXPAND_SZ {
		return k.SetExpandStringValue("Path", v)
	}
	return k.SetStringValue("Path", v)
}

func addUserPath(dir string) error {
	cur := userPath()
	if pathHasDir(cur, dir) {
		return nil
	}
	next := strings.TrimSuffix(cur, ";")
	if next != "" {
		next += ";"
	}
	if err := setUserPath(next + dir); err != nil {
		return err
	}
	announceEnvChange()
	return nil
}

func removeUserPath(dir string) error {
	cur := userPath()
	parts := strings.Split(cur, ";")
	out := parts[:0]
	for _, p := range parts {
		if p != "" && samePath(expandWindowsEnv(p), dir) {
			continue
		}
		out = append(out, p)
	}
	next := strings.Join(out, ";")
	if next == cur {
		return nil
	}
	if err := setUserPath(next); err != nil {
		return err
	}
	announceEnvChange()
	return nil
}

// pathDirs splits a Windows PATH, expanding variables and dropping quotes.
func pathDirs(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ";") {
		p = strings.TrimSpace(strings.Trim(p, `"`))
		if p == "" {
			continue
		}
		p = expandWindowsEnv(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

var windowsEnvVar = regexp.MustCompile(`%([^%]+)%`)

// expandWindowsEnv expands both %VAR% (what the registry stores) and $VAR.
func expandWindowsEnv(s string) string {
	s = os.ExpandEnv(s)
	return windowsEnvVar.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[1 : len(m)-1])
	})
}

// pathHasDir reports whether a PATH-like list holds dir.
func pathHasDir(list, dir string) bool {
	for _, p := range pathDirs(list) {
		if p != "" && samePath(p, dir) {
			return true
		}
	}
	return false
}

// announceEnvChange tells running programs (Explorer, new shells) that the environment
// changed, so a terminal opened now finds masir.
func announceEnvChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	name, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	var result uintptr
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(name)), smtoAbortIfHung, 1000, uintptr(unsafe.Pointer(&result)))
}
