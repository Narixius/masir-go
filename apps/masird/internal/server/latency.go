package server

import (
	"context"

	"github.com/narixius/masir-go/apps/masird/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) TestLatency(ctx context.Context, req *pb.TestLatencyRequest) (*pb.TestLatencyResponse, error) {
	ctx, cancel := context.WithCancel(ctx) // a child we can also cancel by hand
	defer cancel()
	if id := req.TestId; id != "" {
		s.testMu.Lock()
		if _, exists := s.tests[id]; exists {
			s.testMu.Unlock()
			return nil, status.Error(codes.AlreadyExists, "test id already running")
		}
		s.tests[id] = cancel
		s.testMu.Unlock()
		defer func() {
			s.testMu.Lock()
			delete(s.tests, id)
			s.testMu.Unlock()
		}()
	}

	d, err := xray.MeasureDelay(ctx, []byte(req.XrayConfigJson), xray.LatencyOptions{URL: req.ProbeUrl})

	if ctx.Err() != nil { // cancelled or timed out: this is an RPC-level outcome
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if err != nil { // the proxy just doesn't work: a normal answer, not an RPC failure
		return &pb.TestLatencyResponse{Error: err.Error()}, nil
	}
	return &pb.TestLatencyResponse{LatencyMs: d.Milliseconds()}, nil
}

func (s *Server) CancelLatencyTest(ctx context.Context, req *pb.CancelLatencyTestRequest) (*pb.CancelLatencyTestResponse, error) {
	s.testMu.Lock()
	cancel, ok := s.tests[req.TestId]
	s.testMu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "no such test")
	}
	cancel() // the running handler sees ctx.Err() and returns Canceled
	return &pb.CancelLatencyTestResponse{}, nil
}
