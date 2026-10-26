package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// rollDuration is how long a digit takes to roll to its new value.
const rollDuration = 250 * time.Millisecond

var easeRoll = ui.CubicBezier(0.2, 0.9, 0.25, 1)

// roll is what one digit slot remembers between frames: the digit it shows, and where its
// reel is heading, in digits along an endless reel (so it can keep turning the same way).
type roll struct {
	digit int
	pos   float32
}

// rolling is the number of the last frame a root remembers, to know which way it moved.
type rolling struct{ value float64 }

// rollingNumber draws text, in which every digit is a reel that rolls to its new value when
// it changes, up as the number grows and down as it shrinks. value is the number the text
// stands for, which says the direction; the other characters are drawn still. Digits are
// matched from the right, so a number that gains a digit keeps its units where they were.
func rollingNumber(c *ui.Context, key string, value float64, text string, size, line float32, weight int, color ui.Color, monospace bool) {
	root := ui.Row(c.Key("roll-" + key)).Shrink(0).AlignItems(ui.Center).Label(text)
	prev := ui.Local(root, "value", func() rolling { return rolling{value: value} })
	up := value >= prev.value
	prev.value = value

	runes := []rune(text)
	root.Children(func() {
		for i, r := range runes {
			fromRight := len(runes) - 1 - i
			if r < '0' || r > '9' {
				styled(c, string(r), size, line, weight, color, monospace)
				continue
			}
			digitSlot(c, fromRight, int(r-'0'), up, size, line, weight, color, monospace)
		}
	})
}

// styled is one still character of a rolling number.
func styled(c *ui.Context, s string, size, line float32, weight int, color ui.Color, monospace bool) ui.Element {
	if monospace {
		return mono(c, s, size, line, weight, color)
	}
	return label(c, s, size, line, weight, color).FontFeatures("tnum")
}

// digitSlot is the window one digit shows through: as wide as the digit, one line high,
// with the reel moving behind it.
func digitSlot(c *ui.Context, fromRight, digit int, up bool, size, line float32, weight int, color ui.Color, monospace bool) {
	slot := ui.Box(c.Key(fromRight)).Height(line).Shrink(0).ClipY()
	st := ui.Local(slot, "roll", func() roll { return roll{digit: digit, pos: float32(digit)} })
	if st.digit != digit {
		step := (digit - st.digit + 10) % 10 // forward along the reel
		if !up {
			step -= 10 // or backward
		}
		if step == 0 {
			step = 10
		}
		st.pos += float32(step)
		st.digit = digit
	}
	y := slot.AnimateWith("y", st.pos, rollDuration, easeRoll)
	slot.Children(func() {
		// What the slot is sized by, and what it shows at rest.
		sizer := styled(c, string(rune('0'+digit)), size, line, weight, color, monospace)
		if y == st.pos {
			return
		}
		sizer.Opacity(0)
		// The digits on the reel around the one in the window, offset by how far it has rolled.
		lo := int(floor32(y)) - 1
		for k := lo; k <= lo+3; k++ {
			d := ((k % 10) + 10) % 10
			t := styled(c, string(rune('0'+d)), size, line, weight, color, monospace)
			t.Absolute().Left(0).Top((float32(k) - y) * line)
		}
	})
}

func floor32(f float32) float32 {
	i := float32(int(f))
	if f < i {
		i--
	}
	return i
}

// speedParts splits a speed into the number and the unit that follows it.
func speedParts(bps uint64) (num, unit string) {
	s := speed(bps)
	i := strings.IndexAny(s, "KM")
	return s[:i], s[i:]
}
