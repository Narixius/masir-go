//go:build windows

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The command-line tool on Windows is the app's own bin directory on the user's PATH:
// nothing to copy, so an update of the app carries the command with it.

const userEnvKey = `Environment`

func cliCommand() string { return "masir.exe" }

func cliStatusNow(bin string) cliStatus {
	st := cliStatus{path: filepath.Join(filepath.Dir(bin), cliCommand()), dir: filepath.Dir(bin)}
	st.installed = pathHasDir(userPath(), st.dir)
	if !st.installed {
		if p, err := exec.LookPath(cliCommand()); err == nil && samePath(p, bin) {
			st.installed, st.onPath = true, true
			return st
		}
	}
	st.onPath = st.installed
	return st
}

// installCLI adds the app's bin directory to the user's PATH. It changes nothing
// machine-wide, so it asks for no permission.
func installCLI(bin string) (cliStatus, error) {
	if bin == "" || !fileExists(bin) {
		return cliStatus{}, errors.New("the app ships no masir executable to install")
	}
	dir := filepath.Dir(bin)
	if err := addUserPath(dir); err != nil {
		return cliStatus{}, err
	}
	return cliStatusNow(bin), nil
}

func removeCLI(bin string) error {
	return removeUserPath(filepath.Dir(bin))
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
		if p != "" && samePath(p, dir) {
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

// pathHasDir reports whether a PATH-like list holds dir.
func pathHasDir(list, dir string) bool {
	for _, p := range strings.Split(list, ";") {
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
