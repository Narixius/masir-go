//go:build !darwin

package main

import "testing"

// TestAWindowFollowsTheHeightEveryFrame checks the platform switch: a window of its own,
// on Windows and Linux, takes the height with the animation; only the macOS popover keeps
// the one-resize path.
func TestAWindowFollowsTheHeightEveryFrame(t *testing.T) {
	var s shell
	s.platformReady()
	if !s.frameSize {
		t.Fatal("a window of its own should follow the height every frame")
	}
}
