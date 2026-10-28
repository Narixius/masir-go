package service

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// InstallDir holds the service's own copy of masir, which only Administrators can
// change.
func InstallDir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "masir")
}

// BinaryPath is the masir executable the service runs.
func BinaryPath() string { return filepath.Join(InstallDir(), "masir.exe") }

// Supported reports whether masir can install its daemon as a service on this OS.
func Supported() bool { return true }

// Installed asks the Service Control Manager, with query-only access, so it works
// without Administrator rights.
func Installed() bool {
	h, _, ok := openService(windows.SERVICE_QUERY_STATUS)
	if ok {
		windows.CloseServiceHandle(h)
	}
	return ok
}

// Status reports whether the service exists and is running. Windows has no separate
// "enabled" a user can toggle in the same way, so an installed automatic service counts
// as enabled.
func Status() Info {
	h, st, ok := openService(windows.SERVICE_QUERY_STATUS)
	if !ok {
		return Info{}
	}
	defer windows.CloseServiceHandle(h)
	return Info{Installed: true, Running: st == windows.SERVICE_RUNNING, Enabled: true}
}

// openService opens the SCM and the masir service. The returned handle is the service;
// the SCM is closed before returning.
func openService(access uint32) (windows.Handle, uint32, bool) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, 0, false
	}
	defer windows.CloseServiceHandle(scm)
	name, err := windows.UTF16PtrFromString(WindowsName)
	if err != nil {
		return 0, 0, false
	}
	h, err := windows.OpenService(scm, name, access)
	if err != nil {
		return 0, 0, false
	}
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &status); err != nil {
		windows.CloseServiceHandle(h)
		return 0, 0, false
	}
	return h, status.CurrentState, true
}
