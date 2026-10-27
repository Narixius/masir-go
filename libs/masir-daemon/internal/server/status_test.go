package server

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func statusOf(t *testing.T, s *Server) *pb.Status {
	t.Helper()
	res, err := s.GetStatus(context.Background(), &pb.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return res.Status
}

func TestStatusForUI(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)

	st := statusOf(t, s)
	if st.Privileged || st.TunAvailable || st.TunUnavailableReason != "ROOT_REQUIRED" || st.ServedUser != "tester" {
		t.Fatalf("unprivileged status = %+v", st)
	}
	if st.XrayRunning || st.ProxyRunning || st.Settings.GetPower() {
		t.Fatalf("nothing should run before power on: %+v", st)
	}

	on(t, s)
	st = statusOf(t, s)
	want := fmt.Sprintf("127.0.0.1:%d", s.state.Settings.ProxyPort)
	if !st.XrayRunning || !st.ProxyRunning || st.ProxyAddress != want || st.TunRunning || !st.Settings.GetPower() {
		t.Fatalf("powered status = %+v, want proxy on %s", st, want)
	}

	subA, idsA := addSub(t, s, "Main")
	subB, idsB := addSub(t, s, "Other")
	_, _ = s.SelectConfig(ctx, &pb.SelectConfigRequest{SubscriptionId: subA, ConfigId: idsA["Main"]})
	_, _ = s.AddRouting(ctx, &pb.AddRoutingRequest{RuleTag: "pin", RuleJson: `{"domain":["full:p.test"]}`, Target: "config:" + subB + "/" + idsB["Other"]})
	if st = statusOf(t, s); st.ActiveConfigName != "Main" || st.ActiveStale || st.FellBackRules != 0 {
		t.Fatalf("selected status = %+v", st)
	}

	_, _ = s.RemoveSubscription(ctx, &pb.RemoveSubscriptionRequest{Id: subA})
	_, _ = s.RemoveSubscription(ctx, &pb.RemoveSubscriptionRequest{Id: subB})
	if st = statusOf(t, s); !st.ActiveStale || st.ActiveConfigId != idsA["Main"] || st.FellBackRules != 1 {
		t.Fatalf("after removals = %+v; want stale selection and one fallen-back rule", st)
	}
}

func TestWatchStatusStreamsChanges(t *testing.T) {
	s := newTestServer(t)
	client := dialInProcess(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.WatchStatus(ctx, &pb.WatchStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.Status.XrayRunning {
		t.Fatalf("first status = %+v, %v", first, err)
	}

	on(t, s)
	for {
		next, err := stream.Recv()
		if err != nil {
			t.Fatalf("waiting for the power change: %v", err)
		}
		if next.Status.ProxyRunning {
			break
		}
	}

	if _, err := client.Shutdown(ctx, &pb.ShutdownRequest{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Stopped():
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown didn't signal Stopped")
	}
	// Watchers end when the daemon stops, so a UI notices instead of hanging.
	for {
		if _, err := stream.Recv(); err != nil {
			break
		}
	}
}

func TestShutdownStopsXrayBeforeReturning(t *testing.T) {
	s := newTestServer(t)
	on(t, s)
	if !s.xray.Running() {
		t.Fatal("setup: xray didn't start")
	}
	if _, err := s.Shutdown(context.Background(), &pb.ShutdownRequest{}); err != nil {
		t.Fatal(err)
	}
	if s.xray.Running() {
		t.Fatal("TUN and proxy still run after Shutdown returned")
	}
}

func TestBackgroundFailureIsReportedAndRetried(t *testing.T) {
	s := newTestServer(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := busy.Addr().(*net.TCPAddr).Port

	// As if the daemon booted with power on and something else held the proxy port.
	s.mu.Lock()
	s.state.Settings.Power = true
	s.state.Settings.ProxyPort = port
	_ = s.reconcileLocked(context.Background())
	s.mu.Unlock()
	if st := statusOf(t, s); st.LastError == "" || st.ProxyRunning {
		t.Fatalf("status = %+v; want the failure reported", st)
	}

	busy.Close()
	s.mu.Lock()
	err = s.reconcileLocked(context.Background()) // what the retry loop does
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if st := statusOf(t, s); st.LastError != "" || !st.ProxyRunning {
		t.Fatalf("status after retry = %+v", st)
	}
}

func dialInProcess(t *testing.T, s *Server) pb.DaemonServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterDaemonServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return pb.NewDaemonServiceClient(conn)
}
