package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Flags are drawn, not typed: Windows' emoji font has no flag glyphs, and some Linux
// systems have no emoji font at all. The drawings are the 4x3 SVGs of lipis/flag-icons
// (see resources/flags.LICENSE.txt), packed to keep them one small file.
//
//go:embed flags.json.gz
var flagFiles []byte

var (
	flagOnce sync.Once
	flagSrc  map[string]string
	flagSVG  sync.Map // upper-case code -> *ui.SVG
)

// flagData returns the SVG source of a country's flag, by its two-letter code.
func flagData(code string) string {
	flagOnce.Do(loadFlagFiles)
	return flagSrc[strings.ToLower(code)]
}

// loadFlagFiles unpacks the flag drawings once, at launch, rather than in the first frame
// that shows one.
func loadFlagFiles() {
	r, err := gzip.NewReader(bytes.NewReader(flagFiles))
	if err != nil {
		return
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &flagSrc)
}

func init() { flagOnce.Do(loadFlagFiles) }

// flagDrawing returns the flag of a two-letter code, parsed once, or nil when the app has
// no drawing for it.
func flagDrawing(code string) *ui.SVG {
	code = strings.ToUpper(code)
	if v, ok := flagSVG.Load(code); ok {
		svg, _ := v.(*ui.SVG)
		return svg
	}
	src := flagData(code)
	if src == "" {
		return nil
	}
	svg, err := ui.ParseSVG([]byte(src))
	if err != nil {
		return nil
	}
	flagSVG.Store(code, svg)
	return svg
}

// flagElement draws a flag as a square icon of side size, as the tiles and badges that
// hold one are square: a 4:3 drawing fills it by cropping its sides. flag may be a flag
// emoji (as the server names use) or a two-letter code (as the rules use); when the app has
// no drawing, the emoji itself is drawn, and the system's fonts do what they can with it.
func flagElement(c *ui.Context, flag string, size float32) ui.Element {
	if svg := flagDrawing(flagCode(flag)); svg != nil {
		// Its corners curve like its tile's, so it sits in the tile as a picture in a
		// frame rather than a sharp rectangle in a rounded one.
		return ui.Image(c, svg).Size(size, size).Fit(ui.Cover).Radius(size * 0.22)
	}
	return ui.Box(c).Size(size, size).Center().Shrink(0).Children(func() {
		ui.Text(c, flag).FontSize(size * 0.75).FixedLineHeight(size * 0.75)
	})
}
