package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/egoist/mygo"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// updateTray shows the state in the menu bar: the plane is filled and green while masir is on.
func (a *app) updateTray(t *mygo.Tray) {
	if t == nil {
		return
	}
	on := a.connected()
	if on != a.trayOn {
		a.trayOn = on
		if icon, err := trayIcon(on); err == nil {
			// Off: black, which the menu bar tints. On: green, which it must leave as it is.
			_ = t.SetIcon(icon, !on)
		}
	}
	t.SetToolTip(a.tooltip())
}

func (a *app) tooltip() string {
	mode := "Tunnel"
	if a.fallback() == pb.Fallback_FALLBACK_DIRECT {
		mode = "Direct"
	}
	if a.connected() {
		s := fmt.Sprintf("Masir: Connected · %s", mode)
		if srv, ok := a.active(); ok {
			_, name, _ := serverTitle(srv.cfg.GetName())
			s += " (" + name + ")"
		}
		return s
	}
	return "Masir: Disconnected · " + mode
}

// trayGreen is the plane while masir is on, the same green as the status dot.
var trayGreen = color.NRGBA{R: 0, G: 214, B: 136, A: 255}

// trayIcon draws the paper plane: an outline while masir is off, black on transparent for
// macOS to tint, and filled and green while it is on.
func trayIcon(filled bool) ([]byte, error) {
	const size, ss = 32, 4 // pixels, and samples per pixel side
	// The plane's outline, in a 32x32 box.
	poly := [][2]float64{{4, 15}, {28, 4}, {22, 28}, {15, 19}}
	inside := func(x, y float64) bool {
		in := false
		for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
			if (poly[i][1] > y) != (poly[j][1] > y) &&
				x < (poly[j][0]-poly[i][0])*(y-poly[i][1])/(poly[j][1]-poly[i][1])+poly[i][0] {
				in = !in
			}
		}
		return in
	}
	// Distance from a point to the polygon's edges, for the outline.
	edge := func(x, y float64) float64 {
		best := math.MaxFloat64
		for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
			ax, ay, bx, by := poly[j][0], poly[j][1], poly[i][0], poly[i][1]
			dx, dy := bx-ax, by-ay
			t := math.Max(0, math.Min(1, ((x-ax)*dx+(y-ay)*dy)/(dx*dx+dy*dy)))
			best = math.Min(best, math.Hypot(x-(ax+t*dx), y-(ay+t*dy)))
		}
		return best
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			hits := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x, y := float64(px)+(float64(sx)+0.5)/ss, float64(py)+(float64(sy)+0.5)/ss
					if filled && inside(x, y) || !filled && edge(x, y) < 1.3 {
						hits++
					}
				}
			}
			if hits > 0 {
				ink := color.NRGBA{A: 255}
				if filled {
					ink = trayGreen
				}
				ink.A = uint8(255 * hits / (ss * ss))
				img.SetNRGBA(px, py, ink)
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
