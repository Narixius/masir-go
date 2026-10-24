//go:build windows && amd64

package wintun

import _ "embed"

//go:embed wintun_amd64.dll
var dll []byte
