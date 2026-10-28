package main

// The panel remembers the height its closed main page drew, in app user data, so the
// first open of the next launch starts at that height instead of a guess and shrinking a
// frame later. Every later open in a process uses the height it drew itself.

import (
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
)

// panelHeightFile is where the drawn height is remembered, beside cli-install.
func panelHeightFile() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "panel-height")
}

// loadPanelHeight reads the height the previous launch drew, into the height the panel
// opens at. A missing, corrupt or implausible file is ignored: the panel then opens at
// defaultPanelHeight and the first frame remembers the real one.
func (a *app) loadPanelHeight() {
	if a.heightFile == "" {
		return
	}
	b, err := os.ReadFile(a.heightFile)
	if err != nil {
		return
	}
	h, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 32)
	if err != nil || math.IsNaN(h) || math.IsInf(h, 0) || h < 120 || h > 2000 {
		return
	}
	a.baseHeight = float32(h)
	a.savedHeight = a.baseHeight
}

// rememberPanelHeight writes the height the main page just drew, when it changed. It is
// called inside a frame, so the write itself runs off the main thread with the value
// already captured: a frame must never touch the disk. A later height supersedes a write
// that has not run yet, so a burst of changes (a status arriving just after the first
// frame) leaves the last height on disk.
func (a *app) rememberPanelHeight(h float32) {
	if a.heightFile == "" || abs32(h-a.savedHeight) < 0.5 {
		return
	}
	a.savedHeight = h
	path, n := a.heightFile, a.heightWrites.Add(1)
	a.async(func() {
		a.heightMu.Lock()
		defer a.heightMu.Unlock()
		if a.heightWrites.Load() != n {
			return // newer height drawn before this write ran
		}
		writePanelHeight(path, h)
	})
}

// flushPanelHeight writes the last drawn height at quit, in case a scheduled write had
// not run yet. A panel that never drew leaves the remembered height alone.
func (a *app) flushPanelHeight() {
	if a.heightFile == "" || a.baseHeight <= 0 {
		return
	}
	a.heightWrites.Add(1) // any write still in flight is stale now
	a.heightMu.Lock()
	defer a.heightMu.Unlock()
	writePanelHeight(a.heightFile, a.baseHeight)
}

func writePanelHeight(path string, h float32) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("panel height: %v", err)
		return
	}
	value := strconv.FormatFloat(float64(h), 'f', -1, 32) + "\n"
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		log.Printf("panel height: %v", err)
	}
}
