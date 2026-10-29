package daemon

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// stuckServer never lets its stream end on its own: the client is a program that ignores
// the shutdown, as a hung call or a stream this daemon doesn't own would.
type stuckServer struct {
	pb.UnimplementedDaemonServiceServer
	release chan struct{}
}

func (s *stuckServer) WatchStatus(*pb.WatchStatusRequest, pb.DaemonService_WatchStatusServer) error {
	<-s.release
	return nil
}

// A client that holds a stream open must not keep the process — and the service manager
// waiting for it — until it is killed: gracefulStop ends what is left after the grace.
func TestGracefulStopDoesNotWaitOnAClientForever(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	stuck := &stuckServer{release: make(chan struct{})}
	pb.RegisterDaemonServiceServer(srv, stuck)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := pb.NewDaemonServiceClient(conn).WatchStatus(ctx, &pb.WatchStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream // the call is in flight on the server; its handler blocks

	start := time.Now()
	if gracefulStop(srv, 100*time.Millisecond) {
		t.Fatal("gracefulStop returned as if every call finished; the client is still stuck")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("gracefulStop took %v with a stuck stream; it should end at the grace", took)
	}
	close(stuck.release)
	srv.Stop()
}
