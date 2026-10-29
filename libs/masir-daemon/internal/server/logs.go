package server

import (
	"strings"
	"sync"
	"time"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// logHistory is how many recent entries the daemon keeps for `masir logs`. It is small
// enough to stay cheap and large enough to see what led up to a problem.
const logHistory = 1000

// Log lets the rest of the daemon publish entries that StreamLogs fans out.
func (s *Server) Log(level, msg string) {
	s.logs.publish(&pb.LogEntry{
		TimestampMs: time.Now().UnixMilli(),
		Level:       level,
		Message:     msg,
	})
}

func (s *Server) StreamLogs(req *pb.StreamLogsRequest, stream pb.DaemonService_StreamLogsServer) error {
	min := 0 // no level filter: show everything, including debug Xray messages
	if req.MinLevel != "" {
		min = logLevelRank(req.MinLevel)
	}
	history, ch, unsubscribe := s.logs.subscribeWithHistory()
	defer unsubscribe()

	// Replay the recent past first, so `masir logs` is useful on an idle daemon too.
	limited := history
	if n := int(req.Lines); n > 0 && len(limited) > n {
		limited = limited[len(limited)-n:]
	}
	for _, e := range limited {
		if logLevelRank(e.Level) < min {
			continue
		}
		if err := stream.Send(&pb.StreamLogsResponse{Entry: e}); err != nil {
			return err
		}
	}
	if !req.Follow {
		return nil
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-s.stopped: // a shutting-down daemon sends nothing more
			return nil
		case e := <-ch:
			if logLevelRank(e.Level) < min {
				continue
			}
			if err := stream.Send(&pb.StreamLogsResponse{Entry: e}); err != nil {
				return err
			}
		}
	}
}

// logLevelRank orders levels from quietest to loudest; an empty or unknown level is
// treated as information.
func logLevelRank(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return 0
	case "", "info":
		return 1
	case "warn", "warning":
		return 2
	case "error":
		return 3
	}
	return 1
}

// ValidLogLevel reports whether level is one `masir logs --level` accepts.
func ValidLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "", "debug", "info", "warn", "warning", "error":
		return true
	}
	return false
}

// hub is a minimal pub/sub with a small ring of recent entries. Slow subscribers drop
// messages instead of blocking the daemon.
type hub struct {
	mu      sync.Mutex
	subs    map[chan *pb.LogEntry]struct{}
	history []*pb.LogEntry
}

func newHub() *hub { return &hub{subs: map[chan *pb.LogEntry]struct{}{}} }

// subscribeWithHistory adds a subscriber and returns the history atomically, so an
// entry published during subscription can't be lost or delivered twice.
func (h *hub) subscribeWithHistory() ([]*pb.LogEntry, <-chan *pb.LogEntry, func()) {
	ch := make(chan *pb.LogEntry, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	history := append([]*pb.LogEntry(nil), h.history...)
	h.mu.Unlock()
	return history, ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *hub) publish(e *pb.LogEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, e)
	if len(h.history) > logHistory {
		h.history = h.history[len(h.history)-logHistory:]
	}
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // subscriber too slow, drop
		}
	}
}
