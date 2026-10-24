//go:build windows && !amd64 && !arm64

package wintun

var dll []byte // masir builds Windows for amd64 and arm64 only
