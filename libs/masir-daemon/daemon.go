// Package daemon is masir's daemon: it owns Xray, settings, rules and subscriptions,
// and serves them over gRPC on a local socket. The masir CLI runs it with
// `masir daemon run`, so masir ships as a single executable.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/transport"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/identity"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/server"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/userfs"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/wintun"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// Options configure a daemon run.
type Options struct {
	// User is who a root daemon serves (name or uid); empty means SUDO_UID.
	User string
	// Idle starts with masir off and Xray down until a client turns it on, as the CLI
	// starts the daemon for other work (subscriptions, rules, updates).
	Idle bool
	// Version is reported in status and compared against releases by the updater.
	Version string
	// Context stops the daemon when done, as a Windows service stop does; nil means
	// it runs until a signal or a client stops it.
	Context context.Context
	// NoReexec makes Run return ErrRestart after an update instead of starting the new
	// binary itself, for a supervisor (the Windows service manager) to restart it.
	NoReexec bool
}

// ErrRestart means an update installed a new binary that should now be started.
var ErrRestart = errors.New("restart into the updated binary")

// Run serves until a signal or a client stops the daemon. After an update installs a
// new binary, Run doesn't return: it starts the new binary in place of this process.
func Run(opts Options) error {
	if opts.Version != "" {
		server.Version = opts.Version
	}
	// Recorded now: after an update replaces the binary, Linux would report the old,
	// deleted file instead.
	self, _ := os.Executable()
	parent := opts.Context
	if parent == nil {
		parent = context.Background()
	}
	restart, stillIdle, err := run(parent, opts.User, opts.Idle)
	if err != nil || !restart {
		return err
	}
	if opts.NoReexec {
		return ErrRestart
	}
	log.Println("starting the new version")
	args := os.Args
	if !stillIdle { // masir was turned on since; the new version must not start idle
		args = withoutIdle(args)
	}
	return reexec(self, args)
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
func run(parent context.Context, userFlag string, idle bool) (restart, stillIdle bool, err error) {
	id, err := identity.Resolve(userFlag)
	if err != nil {
		return false, false, err
	}
	dir, err := configDir(id)
	if err != nil {
		return false, false, err
	}
	// A root daemon keeps its socket where only root can write; a daemon running as the
	// user keeps it in the user's directory. A running daemon of either kind is in the way.
	userSock := paths.SocketPathIn(dir)
	sock := userSock
	if id.Privileged {
		sock = paths.PrivilegedSocketPath(id.UID)
	}

	// A daemon outlives the terminal that started it (sudo -b leaves it in that session),
	// so closing the terminal must not end it.
	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, running := range uniqueStrings(userSock, paths.PrivilegedSocketPath(id.UID)) {
		if transport.Alive(running) {
			if err := takeOver(ctx, running, id); err != nil {
				return false, false, err
			}
		}
	}
	lis, err := transport.Listen(sock, transport.Peer{UID: id.UID, GID: id.GID, SID: id.SID})
	if err != nil {
		return false, false, err
	}

	if id.Privileged { // only a privileged daemon can bring TUN up; Windows needs Wintun for it
		if err := wintun.Ensure(assetDir()); err != nil {
			log.Printf("write wintun.dll beside the executable: %v", err)
		}
	}
	xc := xray.New(assetDir())
	defer xc.Close() // tears down the TUN and the proxy on every exit path
	daemon := server.New(xc, id)
	if idle {
		daemon.StartIdle()
	}
	defer daemon.CloseAnalytics()
	defer func() {
		if r := recover(); r != nil {
			daemon.TrackPanic(r, debug.Stack())
			panic(r)
		}
	}()
	if err := daemon.Boot(ctx, dir); err != nil {
		lis.Close()
		return false, false, err
	}

	srv := grpc.NewServer(grpc.UnaryInterceptor(daemon.UnaryInterceptor()))
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
		daemon.TrackDaemonError("serve", err)
		daemon.TrackDaemonStopped("error")
		return false, false, err
	}
	reason := "signal"
	if daemon.RestartRequested() {
		reason = "update"
	}
	daemon.TrackDaemonStopped(reason)
	return daemon.RestartRequested(), daemon.Idle(), nil
}

// configDir is the served user's masir directory. As root it's derived from their home
// (and handed to them); otherwise it's the same ConfigDir the CLI uses.
func configDir(id identity.Identity) (string, error) {
	if !id.Privileged {
		return paths.ConfigDir()
	}
	// Root works inside a directory its user controls: pin it, so nothing the user
	// swaps in (a symlink, say) can steer the daemon's file operations elsewhere.
	dir := paths.ConfigDirOf(id.Home)
	return dir, userfs.Pin(dir, id.UID, id.GID)
}

// assetDir holds geoip.dat/geosite.dat next to the binary; a service's working
// directory is "/", so "." would find nothing.
func assetDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	// The macOS service wrapper starts a symlink to the real binary, and the geo data
	// sits beside the real binary, not inside the wrapper.
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
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

// uniqueStrings drops repeats (Windows has one pipe for every kind of daemon).
func uniqueStrings(in ...string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
