package server

import (
	"context"
	"testing"
	"time"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// A supervisor's stop (launchd's bootout, systemd's stop) reaches the daemon as a signal,
// not as a Shutdown call. It must still end the streams clients hold — the app keeps a
// status and a traffic stream open — or the graceful stop waits until the supervisor
// kills the process, and the install that asked for it looks stuck.
func TestBeginShutdownEndsEveryStream(t *testing.T) {
	s := newTestServer(t)
	on(t, s)
	if !s.xray.Running() {
		t.Fatal("setup: xray didn't start")
	}
	client := dialInProcess(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := client.WatchStatus(ctx, &pb.WatchStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	logs, err := client.StreamLogs(ctx, &pb.StreamLogsRequest{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	// Let both handlers start, so the stop has streams to end.
	if _, err := status.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Recv(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	s.BeginShutdown()
	s.BeginShutdown() // a signal can race a client's Shutdown; a second call must be safe

	ended := make(chan string, 2)
	go func() {
		for {
			if _, err := status.Recv(); err != nil {
				ended <- "status"
				return
			}
		}
	}()
	go func() {
		for {
			if _, err := logs.Recv(); err != nil {
				ended <- "logs"
				return
			}
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-ended:
		case <-time.After(3 * time.Second):
			t.Fatalf("%d of 2 streams still running after BeginShutdown", 2-i)
		}
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("streams took %v to end after BeginShutdown", took)
	}
	if s.xray.Running() {
		t.Fatal("TUN and proxy still run after BeginShutdown")
	}
	select {
	case <-s.Stopped():
	case <-time.After(time.Second):
		t.Fatal("BeginShutdown didn't signal Stopped")
	}
}
