package service

import "golang.org/x/sys/windows"

// Supported reports whether masir can install its daemon as a service on this OS.
func Supported() bool { return true }

// Installed asks the Service Control Manager, with query-only access, so it works
// without Administrator rights.
func Installed() bool {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(scm)
	name, err := windows.UTF16PtrFromString(WindowsName)
	if err != nil {
		return false
	}
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	windows.CloseServiceHandle(h)
	return true
}
