// Package service names the daemon's OS service, shared by the daemon (to report whether
// it's installed) and the CLI (to install it).
package service

// Label is the launchd label of the daemon's service on macOS.
const Label = "com.masir.masird"

// WindowsName is the daemon's service name in the Windows Service Control Manager.
const WindowsName = "masir"
