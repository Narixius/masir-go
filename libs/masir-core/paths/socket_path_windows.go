//go:build windows

package paths

const pipe = `\\.\pipe\masir-daemon`

// SocketPath returns the daemon's Windows named pipe endpoint.
func SocketPath() (string, error) { return pipe, nil }

// SocketPathIn ignores dir: the pipe namespace is machine-wide.
func SocketPathIn(string) string { return pipe }
