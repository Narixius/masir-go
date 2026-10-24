package server

import (
	"sync"
	"time"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// Log lets the rest of the daemon publish entries that StreamLogs fans out.
func (s *Server) Log(level, msg string) {
	s.logs.publish(&pb.LogEntry{
		TimestampMs: time.Now().UnixMilli(),
		Level:       level,
		Message:     msg,
	})
}

func (s *Server) StreamLogs(req *pb.StreamLogsRequest, stream pb.DaemonService_StreamLogsServer) error {
	if !req.Follow {
		return nil // nothing buffered yet; add a ring buffer later if you want history
	}
	ch, unsubscribe := s.logs.subscribe()
	defer unsubscribe()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case e := <-ch:
			if err := stream.Send(&pb.StreamLogsResponse{Entry: e}); err != nil {
				return err
			}
		}
	}
}

// hub is a minimal pub/sub. Slow subscribers drop messages instead of blocking the daemon.
type hub struct {
	mu   sync.Mutex
	subs map[chan *pb.LogEntry]struct{}
}

func newHub() *hub { return &hub{subs: map[chan *pb.LogEntry]struct{}{}} }

func (h *hub) subscribe() (<-chan *pb.LogEntry, func()) {
	ch := make(chan *pb.LogEntry, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *hub) publish(e *pb.LogEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // subscriber too slow, drop
		}
	}
}
