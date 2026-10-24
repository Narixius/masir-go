package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/narixius/masir-go/apps/masird/internal/atomicfile"
	"github.com/narixius/masir-go/apps/masird/internal/identity"
	"github.com/narixius/masir-go/apps/masird/internal/server"
	"github.com/narixius/masir-go/apps/masird/internal/xray"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/transport"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func main() {
	userFlag := flag.String("user", "", "when running as root, the user to serve (name or uid); defaults to SUDO_UID")
	idle := flag.Bool("idle", false, "start with masir off and Xray down until a client turns it on, as the CLI starts masird for other work")
	flag.Parse()
	// Recorded now: after an update replaces the binary, Linux would report the old,
	// deleted file instead.
	self, _ := os.Executable()
	restart, stillIdle, err := run(*userFlag, *idle)
	if err != nil {
		log.Fatal(err)
	}
	if restart {
		log.Println("starting the new version")
		args := os.Args
		if !stillIdle { // masir was turned on since; the new version must not start idle
			args = withoutIdle(args)
		}
		log.Fatal(reexec(self, args))
	}
}

// withoutIdle drops the -idle flag from a command line.
func withoutIdle(args []string) []string {
	out := []string{}
	for _, a := range args {
		if a != "-idle" && a != "--idle" && a != "-idle=true" && a != "--idle=true" {
			out = append(out, a)
		}
	}
	return out
}

// run serves until asked to stop. It reports whether to start the binary again, after
// an update installed a new one (by then the TUN and proxy are torn down), and whether
// the daemon was still idle.
func run(userFlag string, idle bool) (restart, stillIdle bool, err error) {
	id, err := identity.Resolve(userFlag)
	if err != nil {
		return false, false, err
	}
	dir, err := configDir(id)
	if err != nil {
		return false, false, err
	}
	sock := paths.SocketPathIn(dir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if transport.Alive(sock) {
		if err := takeOver(ctx, sock, id); err != nil {
			return false, false, err
		}
	}
	lis, err := transport.Listen(sock, id.UID, id.GID)
	if err != nil {
		return false, false, err
	}

	xc := xray.New(assetDir())
	defer xc.Close() // tears down the TUN and the proxy on every exit path
	daemon := server.New(xc, id)
	if idle {
		daemon.StartIdle()
	}
	if err := daemon.Boot(ctx, dir); err != nil {
		lis.Close()
		return false, false, err
	}

	srv := grpc.NewServer()
	pb.RegisterDaemonServiceServer(srv, daemon)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	reflection.Register(srv)
	go func() {
		select {
		case <-ctx.Done():
		case <-daemon.Stopped():
		}
		log.Println("shutting down")
		srv.GracefulStop() // waits for in-flight calls, including the Shutdown that asked for this
	}()

	log.Printf("serving %s (uid %d, root: %v) on %s", id.Name, id.UID, id.Privileged, sock)
	if err := srv.Serve(lis); err != nil {
		return false, false, err
	}
	return daemon.RestartRequested(), daemon.Idle(), nil
}

// configDir is the served user's masir directory. As root it's derived from their home
// (and handed to them); otherwise it's the same ConfigDir the CLI uses.
func configDir(id identity.Identity) (string, error) {
	if !id.Privileged {
		return paths.ConfigDir()
	}
	atomicfile.SetOwner(id.UID, id.GID)
	dir := paths.ConfigDirOf(id.Home)
	return dir, atomicfile.MkdirAll(dir)
}

// assetDir holds geoip.dat/geosite.dat next to the binary; a service's working
// directory is "/", so "." would find nothing.
func assetDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// takeOver asks a running daemon to step aside. Only a root daemon takes over: that's
// the hand-over after the user grants root for TUN. A second background daemon refuses.
func takeOver(ctx context.Context, sock string, id identity.Identity) error {
	if !id.Privileged {
		return errors.New("masird is already running")
	}
	conn, err := grpc.NewClient("passthrough:///masird",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return transport.DialContext(ctx, sock)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := pb.NewDaemonServiceClient(conn).Shutdown(callCtx, &pb.ShutdownRequest{}); err != nil {
		return fmt.Errorf("ask the running daemon to stop: %w", err)
	}
	log.Println("asked the running daemon to stop; taking over")
	deadline := time.Now().Add(10 * time.Second)
	for transport.Alive(sock) {
		if time.Now().After(deadline) {
			return errors.New("the running daemon didn't stop within 10s")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}
