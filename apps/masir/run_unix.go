//go:build !windows

package main

import masird "github.com/narixius/masir-go/libs/masir-daemon"

func runDaemon(opts masird.Options) error { return masird.Run(opts) }
