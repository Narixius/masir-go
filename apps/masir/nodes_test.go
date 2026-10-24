package main

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// fakeDaemon answers latency tests only when told to, and records which were cancelled.
type fakeDaemon struct {
	pb.DaemonServiceClient
	mu        sync.Mutex
	started   map[string]int
	cancelled map[string]int
	answer    map[string]chan struct{}
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{started: map[string]int{}, cancelled: map[string]int{}, answer: map[string]chan struct{}{}}
}

func (f *fakeDaemon) gate(cfg string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.answer[cfg] == nil {
		f.answer[cfg] = make(chan struct{})
	}
	return f.answer[cfg]
}

func (f *fakeDaemon) TestSubscriptionLatency(ctx context.Context, in *pb.TestSubscriptionLatencyRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.TestSubscriptionLatencyResponse], error) {
	cfg := in.ConfigIds[0]
	f.mu.Lock()
	f.started[cfg]++
	f.mu.Unlock()
	return &fakeStream{ctx: ctx, f: f, cfg: cfg, gate: f.gate(cfg)}, nil
}

func (f *fakeDaemon) counts(cfg string) (started, cancelled int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[cfg], f.cancelled[cfg]
}

type fakeStream struct {
	grpc.ClientStream
	ctx  context.Context
	f    *fakeDaemon
	cfg  string
	gate chan struct{}
	sent bool
}

func (s *fakeStream) Recv() (*pb.TestSubscriptionLatencyResponse, error) {
	if s.sent {
		return nil, io.EOF
	}
	select {
	case <-s.ctx.Done():
		s.f.mu.Lock()
		s.f.cancelled[s.cfg]++
		s.f.mu.Unlock()
		return nil, s.ctx.Err()
	case <-s.gate:
		s.sent = true
		return &pb.TestSubscriptionLatencyResponse{ConfigId: s.cfg, Result: &pb.LatencyResult{LatencyMs: 42}}, nil
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProberCancelsRowsThatScrollAway(t *testing.T) {
	f := newFakeDaemon()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &prober{c: f, ctx: ctx, inflight: map[string]inflight{}, results: map[string]string{},
		done: map[string]bool{}, visible: map[string]bool{}, sem: make(chan struct{}, probeWorkers)}
	p.picker = ui.NewPicker(ui.PickParams{Message: "x"})

	p.onVisible([]string{"s/a", "s/b"})
	eventually(t, "a and b to start", func() bool {
		a, _ := f.counts("a")
		b, _ := f.counts("b")
		return a == 1 && b == 1
	})

	// a scrolls out, c scrolls in: a is cancelled, c starts, b keeps going.
	p.onVisible([]string{"s/b", "s/c"})
	eventually(t, "a cancelled and c started", func() bool {
		_, ac := f.counts("a")
		c, _ := f.counts("c")
		return ac == 1 && c == 1
	})
	if _, bc := f.counts("b"); bc != 0 {
		t.Fatal("b is still on screen; its test must keep running")
	}

	// b answers: it's done and isn't tested again while it stays on screen.
	close(f.gate("b"))
	eventually(t, "b to finish", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.done["s/b"] })
	p.onVisible([]string{"s/b", "s/c"})
	if b, _ := f.counts("b"); b != 1 {
		t.Fatalf("b was tested %d times; a finished row shouldn't be retested by scrolling", b)
	}

	// l retests b; a scrolling back in is tested again since it never finished.
	p.retest("s/b")
	p.onVisible([]string{"s/a", "s/b", "s/c"})
	eventually(t, "b retested and a restarted", func() bool {
		a, _ := f.counts("a")
		b, _ := f.counts("b")
		return a == 2 && b == 2
	})

	// a retests everything on screen: c, still running, restarts.
	p.retestAll()
	eventually(t, "c restarted", func() bool { c, cc := f.counts("c"); return c == 2 && cc == 1 })

	// Quitting cancels whatever is left.
	cancel()
	p.wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done["s/c"] {
		t.Fatal("a cancelled test must not record a result")
	}
}
