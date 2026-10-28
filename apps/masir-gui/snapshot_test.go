package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
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
		{"servers-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click(ltr("United Kingdom")) }},
		{"rules-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("RULES") }},
		{"settings-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("Settings") }},
		{"settings-middle", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 300, 0, 430)
			tt.Advance(time.Second)
		}},
		{"settings-logging", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 300, 0, 650)
			tt.Advance(time.Second)
		}},
		{"settings-select", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 300, 0, 430)
			tt.Advance(time.Second)
			_ = tt.Click("Warning") // the log level select's trigger
			tt.Advance(100 * time.Millisecond)
		}},
		{"update-available", true, func(a *app, tt *ui.Tester) {
			a.upd.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.upd.phase = updateAvailable
		}},
		{"update-downloading", true, func(a *app, tt *ui.Tester) {
			a.upd.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.upd.job = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, BytesDone: 42, BytesTotal: 100}
			a.upd.parts, a.upd.phase = 2, updateDownloading
		}},
		{"update-paused", true, func(a *app, tt *ui.Tester) {
			a.upd.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.upd.job = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_PAUSED, BytesDone: 42, BytesTotal: 100}
			a.upd.parts, a.upd.phase = 2, updatePaused
		}},
		{"update-app-downloading", true, func(a *app, tt *ui.Tester) {
			a.upd.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.upd.parts, a.upd.phase = 2, updateAppDownloading
			a.upd.progress, a.upd.total = 74, 100
		}},
		{"settings-update", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			a.upd.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, CheckedAtMs: time.Now().UnixMilli()}
			a.upd.phase = updateAvailable
			tt.Scroll(panelWidth/2, 400, 0, 1200)
			tt.Advance(time.Second)
		}},
		{"system-dark", true, func(a *app, tt *ui.Tester) {
			_ = tt.Click("Settings")
			tt.Advance(time.Second)
			tt.Scroll(panelWidth/2, 420, 0, 1200)
			tt.Advance(100 * time.Millisecond)
		}},
		{"add-dark", true, func(a *app, tt *ui.Tester) { _ = tt.Click("Add subscription") }},
	}
	for _, s := range shots {
		a := newTestApp(ukServers())
		a.down, a.up = 120<<10, 8<<10
		tt := ui.NewTester(a.view, panelWidth, 600)
		tt.SetDark(s.dark)
		s.do(a, tt)
		if s.name != "rolling-mid" {
			tt.Advance(time.Second) // let the panels finish opening
		}
		tt.SetSize(panelWidth, int(a.restHeight()))
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
