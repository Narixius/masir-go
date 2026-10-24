package wintun

// Ensure puts this build's wintun.dll in dir, the directory of the running executable.
func Ensure(dir string) error { return ensure(dir, dll) }
