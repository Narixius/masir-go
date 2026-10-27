package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestIconTileCentersWhatItHolds checks that an icon and a flag sit in the middle of their
// rounded square, with the same room above and below, whatever the drawing's height.
func TestIconTileCentersWhatItHolds(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Children(func() {
			iconTile(c, paletteFor(c), func() {
				ui.Image(c, iconGlobe).Size(18, 18).Label("icon")
			})
			iconTile(c, paletteFor(c), func() {
				flagElement(c, "🇩🇪", 18, 13.5).Label("flag")
			})
		})
	}, 300, 100)
	tt.Frame()

	side := tw(7)
	for _, tc := range []struct {
		label string
		w, h  float32
	}{
		{"icon", 18, 18},
		{"flag", 18, 13.5},
	} {
		r, ok := tt.Find(tc.label)
		if !ok {
			t.Fatalf("no %s in the tile", tc.label)
		}
		if r.W != tc.w || r.H != tc.h {
			t.Errorf("%s is %v×%v, want %v×%v", tc.label, r.W, r.H, tc.w, tc.h)
		}
		// The tile's 1-unit border leaves its content, and the drawing centers in it.
		top, bottom := r.Y-1, side-1-(r.Y+r.H)
		if abs32(top-bottom) > 0.01 {
			t.Errorf("%s has %v above it and %v below it", tc.label, top, bottom)
		}
	}
}
