package server

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

type fakeLogStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*pb.LogEntry
}

func (f *fakeLogStream) Send(r *pb.StreamLogsResponse) error {
	f.sent = append(f.sent, r.Entry)
	return nil
}

func (f *fakeLogStream) Context() context.Context { return f.ctx }

func TestHubHistoryAndLevels(t *testing.T) {
	s := &Server{logs: newHub()}
	s.Log("info", "one")
	s.Log("warn", "two")
	s.Log("error", "three")

	stream := &fakeLogStream{ctx: context.Background()}
	if err := s.StreamLogs(&pb.StreamLogsRequest{Follow: false, MinLevel: "warn"}, stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.sent) != 2 || stream.sent[0].Message != "two" || stream.sent[1].Message != "three" {
		t.Fatalf("warn history = %+v", stream.sent)
	}

	stream = &fakeLogStream{ctx: context.Background()}
	if err := s.StreamLogs(&pb.StreamLogsRequest{Follow: false, Lines: 1}, stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.sent) != 1 || stream.sent[0].Message != "three" {
		t.Fatalf("last entry = %+v", stream.sent)
	}
}

func TestHubKeepsOnlyRecentHistory(t *testing.T) {
	h := newHub()
	for i := 0; i < logHistory+5; i++ {
		h.publish(&pb.LogEntry{Message: "x"})
	}
	history, _, stop := h.subscribeWithHistory()
	defer stop()
	if len(history) != logHistory {
		t.Fatalf("history length = %d, want %d", len(history), logHistory)
	}
}

func TestLogLevelHelpers(t *testing.T) {
	for level, want := range map[string]int{"debug": 0, "info": 1, "": 1, "warn": 2, "warning": 2, "error": 3} {
		if got := logLevelRank(level); got != want {
			t.Errorf("logLevelRank(%q) = %d, want %d", level, got, want)
		}
	}
	for level, want := range map[string]bool{"": true, "debug": true, "info": true, "warn": true, "error": true, "nope": false} {
		if got := ValidLogLevel(level); got != want {
			t.Errorf("ValidLogLevel(%q) = %v, want %v", level, got, want)
		}
	}
}
