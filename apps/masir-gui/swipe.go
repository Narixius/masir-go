package main

import (
	"math"
	"time"
)

// The swipe of a rule row, as the design has it: two fingers on the touchpad drag the row
// left to show its buttons, with the row following the fingers, resisting like a rubber band
// past either end, and settling open or closed by where it is and how fast it was going.

const (
	swipeWidth       = 92   // how far the row slides to show its buttons
	swipeSensitivity = 1.15 // the row moves this much for each unit of scrolling
	swipeMinRaw      = -swipeWidth - 28
	swipeMaxRaw      = 18

	// The spring that settles the row.
	swipeStiffness = 500
	swipeDamping   = 38
	swipeMass      = 0.45

	// How long without scrolling means the fingers are up, and how long trailing inertia is
	// absorbed after the row starts to settle.
	swipeEnd         = 42 * time.Millisecond
	swipeEndRubber   = 30 * time.Millisecond
	swipeQuiet       = 45 * time.Millisecond
	swipeFlick       = 160 // px/s that decide the way regardless of position
	swipeHistoryTime = 80 * time.Millisecond
)

type swipeMode int

const (
	swipeIdle swipeMode = iota
	swipeTracking
	swipeSettling
)

type swipeSample struct {
	pos float32
	at  time.Time
}

// swipe is one row's gesture state.
type swipe struct {
	mode   swipeMode
	x      float32 // where the row shows: 0 closed, -swipeWidth open
	raw    float32 // where the fingers say it is, before the rubber band
	target float32 // where it is settling to
	vel    float32 // px/s while settling
	open   bool    // it is open, or settling open

	endAt time.Time // the fingers are up when this passes
	quiet time.Time // trailing inertia is absorbed until then
	last  time.Time // when the spring was last advanced
	hist  []swipeSample
}

func rubberband(distance, dimension, c float32) float32 {
	return distance * dimension * c / (dimension + c*abs32(distance))
}

// swipeVisual is where the row shows for where the fingers have taken it: past either end it
// follows less and less.
func swipeVisual(raw float32) float32 {
	switch {
	case raw > 0:
		return rubberband(raw, 16, 0.45)
	case raw < -swipeWidth:
		return -swipeWidth - rubberband(-swipeWidth-raw, 28, 0.45)
	}
	return raw
}

func clamp32(v, lo, hi float32) float32 {
	return float32(math.Max(float64(lo), math.Min(float64(hi), float64(v))))
}

// scroll takes a touchpad scroll of dx and dy, as the system reports them: negative dx is
// the fingers moving left, which opens the row. It reports whether the row took the event; one it
// leaves goes on to scroll the list.
func (s *swipe) scroll(dx, dy float32, now time.Time) bool {
	absX, absY := abs32(dx), abs32(dy)

	if s.mode == swipeSettling {
		counter := (s.target == -swipeWidth && dx > 2 && absX > absY) || (s.target == 0 && dx < -2 && absX > absY)
		if counter { // the person turned it around
			s.begin(now)
			return true
		}
		s.quiet = now.Add(swipeQuiet) // trailing inertia: absorbed
		return true
	}

	if s.mode == swipeIdle {
		if !s.open {
			if absY > absX || dx > -0.3 {
				return false
			}
		} else if (absY > absX*1.5 && absX < 2) || (dx < 0.3 && absY > absX && dx > -0.3) {
			return false
		}
		s.begin(now)
	}

	s.raw = clamp32(s.raw+dx*swipeSensitivity, swipeMinRaw, swipeMaxRaw)
	s.hist = append(s.hist, swipeSample{s.raw, now})
	for len(s.hist) > 2 && now.Sub(s.hist[0].at) > swipeHistoryTime {
		s.hist = s.hist[1:]
	}
	s.x = swipeVisual(s.raw)

	left, right := s.raw <= -swipeWidth, s.raw >= 0
	pushing := (left && dx <= 0) || (right && dx >= 0)
	// Once it is rebounding off an end, trailing events pushing further in must not put that off.
	if !pushing || s.endAt.IsZero() {
		wait := swipeEnd
		if left || right {
			wait = swipeEndRubber
		}
		s.endAt = now.Add(wait)
	}
	return true
}

// begin starts tracking the fingers from where the row is.
func (s *swipe) begin(now time.Time) {
	s.mode = swipeTracking
	s.raw = s.x
	s.vel = 0
	s.quiet, s.endAt = time.Time{}, time.Time{}
	s.hist = []swipeSample{{s.raw, now}}
}

// end is the fingers coming up: the row settles open or closed.
func (s *swipe) end(now time.Time) {
	s.endAt = time.Time{}
	var v float32
	if n := len(s.hist); n >= 2 {
		old, cur := s.hist[0], s.hist[n-1]
		if dt := cur.at.Sub(old.at); dt > 8*time.Millisecond {
			v = (cur.pos - old.pos) / float32(dt.Seconds())
		}
	}
	s.hist = nil

	open, vel := false, v
	switch {
	case s.x < -swipeWidth:
		open, vel = true, float32(math.Max(0, float64(v))) // rebounding: not against the way back
	case s.x > 0:
		open, vel = false, float32(math.Min(0, float64(v)))
	case v < -swipeFlick:
		open = true
	case v > swipeFlick:
		open = false
	default:
		open = s.x < -swipeWidth/2
	}
	s.settle(open, vel, now)
}

// settle sends the row to open or closed, carrying the speed it has.
func (s *swipe) settle(open bool, vel float32, now time.Time) {
	s.mode = swipeSettling
	s.open = open
	s.target = 0
	if open {
		s.target = -swipeWidth
	}
	if s.target == 0 {
		vel = clamp32(vel, -800, 1200)
	} else {
		vel = clamp32(vel, -1200, 800)
	}
	s.vel, s.last = vel, now
}

// advance moves the row on to now and reports whether it is still in motion or waiting on
// the fingers.
func (s *swipe) advance(now time.Time) bool {
	switch s.mode {
	case swipeTracking:
		if !s.endAt.IsZero() && !now.Before(s.endAt) {
			s.end(now)
		}
		return true
	case swipeSettling:
		dt := float32(now.Sub(s.last).Seconds())
		s.last = now
		for dt > 0 { // small steps keep the spring stable
			h := float32(math.Min(float64(dt), 0.001))
			a := (-swipeStiffness*(s.x-s.target) - swipeDamping*s.vel) / swipeMass
			s.vel += a * h
			s.x += s.vel * h
			dt -= h
		}
		if abs32(s.x-s.target) < 0.05 && abs32(s.vel) < 1 {
			s.x, s.vel = s.target, 0
			if !now.Before(s.quiet) {
				s.mode = swipeIdle
				return false
			}
		}
		return true
	}
	return false
}

// close sends the row back; open sends it out.
func (s *swipe) close(now time.Time) {
	if s.x != 0 || s.open {
		s.settle(false, 0, now)
	}
}

// buttonsX is how far the buttons are still to the right of where they rest; they come in as the row goes.
func (s *swipe) buttonsX() float32 { return swipeWidth + s.x }

// buttonsOpacity: the buttons fade in over the first 10 units.
func (s *swipe) buttonsOpacity() float32 {
	switch {
	case s.x >= 0:
		return 0
	case s.x <= -10:
		return 1
	}
	return -s.x / 10
}
