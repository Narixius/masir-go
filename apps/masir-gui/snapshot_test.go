package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSnapshots writes what the panel looks like to PNG files when MASIR_SNAPSHOTS names
// a directory, for looking at without running the app. It checks nothing itself.
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("MASIR_SNAPSHOTS")
	if dir == "" {
		t.Skip("set MASIR_SNAPSHOTS to a directory to write the pictures to")
	}
	shots := []struct {
		name string
		dark bool
		do   func(a *app, tt *ui.Tester)
	}{
		{"rolling-mid", true, func(a *app, tt *ui.Tester) {
			a.latency[serverKey{"s1", "c1"}] = 2075
			a.rules = append(a.rules, a.rules[0])
			tt.Frame()
			tt.Advance(110 * time.Millisecond) // part way through the roll
		}},
		{"main-light", false, func(a *app, tt *ui.Tester) {}},
		{"main-dark", true, func(a *app, tt *ui.Tester) {}},
		{"servers-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click(ltr("🇬🇧 United Kingdom")) }},
		{"rules-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("RULES") }},
		{"settings-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("Settings") }},
		{"add-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("Add subscription") }},
	}
	for _, s := range shots {
		a := newTestApp(ukServers())
		a.onHeight = func(h float32) { a.heightNow = h }
		a.down, a.up = 120<<10, 8<<10
		tt := ui.NewTester(a.view, panelWidth, 600)
		tt.SetDark(s.dark)
		s.do(a, tt)
		if s.name != "rolling-mid" {
			tt.Advance(time.Second) // let the panels finish opening
		}
		tt.SetSize(panelWidth, int(a.heightNow))
		if s.name != "rolling-mid" {
			tt.Advance(time.Second)
		} else {
			tt.Frame()
		}
		f, err := os.Create(filepath.Join(dir, s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
