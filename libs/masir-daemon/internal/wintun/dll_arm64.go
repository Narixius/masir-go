//go:build windows && arm64

package wintun

import _ "embed"

//go:embed wintun_arm64.dll
var dll []byte
