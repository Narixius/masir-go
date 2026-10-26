package main

import "testing"

func TestSpringArrivesWithoutOvershooting(t *testing.T) {
	s := spring{target: 1}
	prev := float32(0)
	for i := 0; i < 400; i++ {
		s.step(1.0 / 120)
		if s.x > 1.0001 {
			t.Fatalf("overshot to %v at step %d", s.x, i)
		}
		if s.x < prev-1e-6 {
			t.Fatalf("went backwards at step %d: %v after %v", i, s.x, prev)
		}
		prev = s.x
	}
	if !s.settled() {
		t.Fatalf("not settled after 3s: %+v", s)
	}
}

func TestSpringSettlesQuickly(t *testing.T) {
	s := spring{target: 1}
	for i := 0; i < 24; i++ { // 0.2s at 120 steps a second
		s.step(1.0 / 120)
	}
	if s.x < 0.98 {
		t.Fatalf("only %v of the way after 0.2s", s.x)
	}
}

func TestSpringTurnsAroundSmoothly(t *testing.T) {
	s := spring{target: 1}
	for i := 0; i < 15; i++ {
		s.step(1.0 / 120)
	}
	s.target = 0 // closed again on the way
	for i := 0; i < 600; i++ {
		s.step(1.0 / 120)
		if s.x < -0.0001 {
			t.Fatalf("overshot below zero: %v", s.x)
		}
	}
}

func TestSpringStepsAreIndependentOfTheirSize(t *testing.T) {
	a, b := spring{target: 1}, spring{target: 1}
	for i := 0; i < 60; i++ {
		a.step(1.0 / 60)
	}
	for i := 0; i < 240; i++ {
		b.step(1.0 / 240)
	}
	if d := abs32(a.x - b.x); d > 0.01 {
		t.Fatalf("1s in 60 steps gave %v, in 240 steps %v", a.x, b.x)
	}
}
