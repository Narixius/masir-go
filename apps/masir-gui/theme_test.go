package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestListHeaderIsOpaque checks that the header pinned over a list hides the rows
// scrolling under it: a translucent tint would let their text show through.
func TestListHeaderIsOpaque(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tt := ui.NewTester(func(c *ui.Context) {
			if p := paletteFor(c); p.listHeader.A != 255 {
				t.Errorf("dark=%v: the list header's alpha is %d, want opaque", dark, p.listHeader.A)
			}
		}, 10, 10)
		tt.SetDark(dark)
		tt.Frame()
	}
}
