// apps/masird/internal/server/server.go
package server

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/narixius/masir-go/apps/masird/internal/compose"
	"github.com/narixius/masir-go/apps/masird/internal/identity"
	"github.com/narixius/masir-go/apps/masird/internal/store"
	"github.com/narixius/masir-go/apps/masird/internal/subs"
	"github.com/narixius/masir-go/apps/masird/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// retryEvery is how often a config that failed to apply in the background is retried.
const retryEvery = 15 * time.Second

type Server struct {
	pb.UnimplementedDaemonServiceServer
	xray      *xray.Core
	id        identity.Identity
	startedAt time.Time

	// mu serializes every change to state, the selection and the running config.
	mu       sync.Mutex
	dir      string
	state    store.State
	resolved []compose.ResolvedRule // user rules as last applied
	running  running                // what the last successful apply brought up
	lastErr  string                 // why what runs doesn't match the state, from a background apply
	config   []byte                 // the composed Xray config, as written to xray.json

	testMu sync.Mutex
	tests  map[string]context.CancelFunc // test ID -> its cancel function

	logs    *hub
	changes *signal
	subs    *subs.Manager // set by Boot

	stopOnce sync.Once
	stopped  chan struct{}
}

type running struct {
	proxy     bool
	proxyAddr string
	tun       bool
}

func New(xc *xray.Core, id identity.Identity) *Server {
	if xc == nil {
		panic("server.New: xray core is nil")
	}
	return &Server{
		xray:      xc,
		id:        id,
		startedAt: time.Now(),
		state:     store.State{Settings: store.DefaultSettings()},
		logs:      newHub(),
		changes:   newSignal(),
		tests:     map[string]context.CancelFunc{},
		stopped:   make(chan struct{}),
	}
}

// Boot loads state from dir, brings Xray to what the settings ask for, and until ctx is
// done keeps subscriptions fresh and retries a config that failed to start. It fails
// only if state can't be loaded, so a UI can still connect and fix a broken config.
func (s *Server) Boot(ctx context.Context, dir string) error {
	st, err := store.Load(dir)
	if err != nil {
		return err
	}
	m, err := subs.NewManager(filepath.Join(dir, "subscriptions"), filepath.Join(dir, "active.json"), s.xray.Dial)
	if err != nil {
		return err
	}
	m.Log = s.Log
	m.OnChange = s.subscriptionsChanged
	s.subs = m

	s.mu.Lock()
	s.dir, s.state = dir, st
	if err := s.reconcileLocked(ctx); err != nil {
		log.Printf("start xray: %v", err)
	}
	s.mu.Unlock()

	go m.Run(ctx)
	go s.retryFailed(ctx)
	return nil
}

// Stopped is closed when a client asks the daemon to shut down.
func (s *Server) Stopped() <-chan struct{} { return s.stopped }

func (s *Server) Shutdown(ctx context.Context, _ *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	s.Log("info", "shutdown requested")
	s.stopOnce.Do(func() { close(s.stopped) })
	return &pb.ShutdownResponse{}, nil
}

func (s *Server) retryFailed(ctx context.Context) {
	t := time.NewTicker(retryEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		if s.lastErr != "" {
			_ = s.reconcileLocked(ctx)
		}
		s.mu.Unlock()
	}
}

var Version = "dev" // overridden via -ldflags "-X .../server.Version=0.1.0"

func (s *Server) GetXrayVersion(ctx context.Context, req *pb.GetXrayVersionRequest) (*pb.GetXrayVersionResponse, error) {
	return &pb.GetXrayVersionResponse{
		Version: s.xray.GetVersion(),
	}, nil
}

func (s *Server) GetXrayConfig(ctx context.Context, _ *pb.GetXrayConfigRequest) (*pb.GetXrayConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.GetXrayConfigResponse{Json: string(s.config)}, nil
}
