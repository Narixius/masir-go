//go:build !windows

package client

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

type fakeDaemon struct {
	pb.UnimplementedDaemonServiceServer
}

func (fakeDaemon) GetXrayVersion(context.Context, *pb.GetXrayVersionRequest) (*pb.GetXrayVersionResponse, error) {
	return &pb.GetXrayVersionResponse{Version: "v1.2.3"}, nil
}

// serve answers on a socket of its own, which is the only one the client looks at.
func serve(t *testing.T) (*grpc.Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mc") // short: unix socket paths are
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := dir + "/daemon.sock"
	oldSockets, oldUser := sockets, userSocket
	sockets = func() ([]string, error) { return []string{sock}, nil }
	userSocket = func() (string, error) { return sock, nil }
	t.Cleanup(func() { sockets, userSocket = oldSockets, oldUser })
	return listen(t, sock), sock
}

func listen(t *testing.T, sock string) *grpc.Server {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	pb.RegisterDaemonServiceServer(srv, fakeDaemon{})
	go srv.Serve(l)
	t.Cleanup(srv.Stop)
	return srv
}

func TestDialReachesTheLiveDaemon(t *testing.T) {
	serve(t)
	if !Alive() {
		t.Fatal("the daemon answers, but Alive says it doesn't")
	}
	conn, c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.GetXrayVersion(ctx, &pb.GetXrayVersionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "v1.2.3" {
		t.Fatalf("version = %q", res.Version)
	}
}

func TestDialSurvivesTheDaemonRestarting(t *testing.T) {
	srv, sock := serve(t)
	conn, c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	srv.Stop()
	if Alive() {
		t.Fatal("Alive after the daemon stopped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.GetXrayVersion(ctx, &pb.GetXrayVersionRequest{}); status.Code(err) == codes.OK {
		t.Fatal("a call succeeded with no daemon")
	}

	os.Remove(sock)
	listen(t, sock)

	// The same client redials on its own once a daemon answers again.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := c.GetXrayVersion(ctx, &pb.GetXrayVersionRequest{}, grpc.WaitForReady(true))
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client never reconnected: %v", err)
		}
	}
}

func TestErrorReasonIgnoresPlainErrors(t *testing.T) {
	if r := ErrorReason(status.Error(codes.Internal, "boom")); r != "" {
		t.Fatalf("reason = %q", r)
	}
}

func TestWaitForGivesUp(t *testing.T) {
	err := WaitFor(context.Background(), func() bool { return false }, 200*time.Millisecond, "never")
	if err == nil || err.Error() != "never" {
		t.Fatalf("err = %v", err)
	}
}
