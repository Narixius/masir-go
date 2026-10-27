package main

import "math"

// spring moves a value to its target the way a critically damped spring does: quickly,
// slowing smoothly, and never past the target. It is the motion of every transition of the
// panel, so that the window and what is in it move together.
type spring struct {
	x, v, target float32
}

// springRate is how stiff the springs are: the larger, the sooner they settle. At 40 a
// spring is within 1% of its target after about 0.18 seconds.
const springRate = 40

// step moves the spring on by dt seconds, exactly, whatever the length of the step.
func (s *spring) step(dt float32) {
	w := float64(springRate)
	e := math.Exp(-w * float64(dt))
	y := float64(s.x - s.target)
	v := float64(s.v)
	s.x = s.target + float32((y+(v+w*y)*float64(dt))*e)
	s.v = float32((v - w*(v+w*y)*float64(dt)) * e)
	if abs32(s.x-s.target) < 0.0005 && abs32(s.v) < 0.005 {
		s.x, s.v = s.target, 0
	}
}

func (s *spring) settled() bool { return s.x == s.target && s.v == 0 }

func abs32(f float32) float32 { return float32(math.Abs(float64(f))) }

// motion is the animated state of the panel: how far each part is open, from 0 to 1.
type motion struct {
	servers, rules, page spring
	// instant skips the animation, for tests.
	instant bool
}

// aim sets where the springs are heading, and reports whether a target changed.
func (m *motion) aim(servers, rules, page float32) (changed bool) {
	for _, t := range []struct {
		s *spring
		v float32
	}{{&m.servers, servers}, {&m.rules, rules}, {&m.page, page}} {
		if t.s.target != t.v {
			changed = true
		}
		t.s.target = t.v
		if m.instant {
			t.s.x, t.s.v = t.v, 0
		}
	}
	return changed
}

// pageClosing reports whether the page over the main one is sliding away.
func (m *motion) pageClosing() bool { return m.page.target == 0 && m.page.x > 0 }

// step advances every spring and reports whether any is still moving.
func (m *motion) step(dt float32) bool {
	m.servers.step(dt)
	m.rules.step(dt)
	m.page.step(dt)
	return !m.settled()
}

func (m *motion) settled() bool {
	return m.servers.settled() && m.rules.settled() && m.page.settled()
}

// listsSettled reports whether the two collapsible lists are at rest, whatever the pages
// do.
func (m *motion) listsSettled() bool { return m.servers.settled() && m.rules.settled() }
