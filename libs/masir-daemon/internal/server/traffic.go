package server

import (
	"time"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

const (
	defaultTrafficInterval = time.Second
	minTrafficInterval     = 250 * time.Millisecond
)

func (s *Server) WatchTraffic(req *pb.WatchTrafficRequest, stream pb.DaemonService_WatchTrafficServer) error {
	every := time.Duration(req.IntervalMs) * time.Millisecond
	if every == 0 {
		every = defaultTrafficInterval
	}
	every = max(every, minTrafficInterval)

	tick := time.NewTicker(every)
	defer tick.Stop()
	var (
		prev     TrafficReading
		prevTime time.Time
	)
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-s.stopped:
			return nil
		case now := <-tick.C:
			cur, ok, err := s.xray.Traffic(stream.Context())
			if err != nil || !ok { // Xray is down or restarting: start counting again once it is back
				prev, prevTime = TrafficReading{}, time.Time{}
				continue
			}
			r := TrafficReading{Up: cur.Up, Down: cur.Down, Valid: true}
			if res := rate(prev, prevTime, r, now); res != nil {
				if err := stream.Send(res); err != nil {
					return err
				}
			}
			prev, prevTime = r, now
		}
	}
}

// TrafficReading is one read of the counters.
type TrafficReading struct {
	Up, Down uint64
	Valid    bool
}

// rate is the speed between two readings, or nil without an earlier one. A counter that
// went down means Xray restarted in between, so that interval counts from zero.
func rate(prev TrafficReading, prevTime time.Time, cur TrafficReading, now time.Time) *pb.WatchTrafficResponse {
	if !prev.Valid {
		return nil
	}
	secs := now.Sub(prevTime).Seconds()
	if secs <= 0 {
		return nil
	}
	delta := func(before, after uint64) uint64 {
		if after < before {
			before = 0
		}
		return uint64(float64(after-before) / secs)
	}
	return &pb.WatchTrafficResponse{
		UplinkBps:     delta(prev.Up, cur.Up),
		DownlinkBps:   delta(prev.Down, cur.Down),
		UplinkTotal:   cur.Up,
		DownlinkTotal: cur.Down,
	}
}
