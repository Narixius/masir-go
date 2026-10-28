// Package service names the daemon's OS service, shared by the daemon (to report whether
// it's installed) and the CLI (to install it).
package service

// Label is the launchd label of the daemon's service on macOS.
const Label = "com.masir.masird"

// WindowsName is the daemon's service name in the Windows Service Control Manager.
const WindowsName = "masir"

// Info is the service as the OS sees it. Installed says its definition exists, Running
// says the service manager is running it now, and Enabled says the user hasn't turned it
// off (macOS Login Items, systemd enablement). The three are different: a plist that
// exists is not a daemon that runs, and a daemon that runs is not one that starts at
// boot.
type Info struct {
	Installed bool
	Running   bool
	Enabled   bool
}
