package server

import (
	"testing"
	"time"
)

func TestRateIsBytesPerSecondBetweenReadings(t *testing.T) {
	t0 := time.Now()
	prev := TrafficReading{Up: 1000, Down: 5000, Valid: true}
	cur := TrafficReading{Up: 1500, Down: 9000, Valid: true}
	got := rate(prev, t0, cur, t0.Add(500*time.Millisecond))
	if got == nil || got.UplinkBps != 1000 || got.DownlinkBps != 8000 || got.UplinkTotal != 1500 || got.DownlinkTotal != 9000 {
		t.Fatalf("got %+v", got)
	}
}

func TestRateWaitsForAFirstReading(t *testing.T) {
	if rate(TrafficReading{}, time.Time{}, TrafficReading{Up: 1, Valid: true}, time.Now()) != nil {
		t.Fatal("a rate without an earlier reading")
	}
}

func TestRateCountsFromZeroAfterARestart(t *testing.T) {
	t0 := time.Now()
	prev := TrafficReading{Up: 9000, Down: 9000, Valid: true}
	cur := TrafficReading{Up: 100, Down: 300, Valid: true}
	got := rate(prev, t0, cur, t0.Add(time.Second))
	if got.UplinkBps != 100 || got.DownlinkBps != 300 {
		t.Fatalf("got %+v", got)
	}
}
