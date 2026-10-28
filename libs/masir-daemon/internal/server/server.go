package server

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/analytics"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/compose"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/identity"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/rulecheck"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
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
	// ruleProblems are the enabled rules the geo data can't build, as of the last check
	// (see gate); suspended is the subset the running config leaves out.
	ruleProblems []rulecheck.Problem
	suspended    map[string]bool

	testMu sync.Mutex
	tests  map[string]context.CancelFunc // test ID -> its cancel function

	analytics *analytics.Client

	logs    *hub
	changes *signal
	subs    *subs.Manager // set by Boot

	upd          *updates    // set by Boot
	restart      atomic.Bool // the daemon stopped to start a newly installed version
	stopTracked  sync.Once   // daemon_stopped is recorded once
	subCount     atomic.Int64
	totalConfigs atomic.Int64
	// idle keeps Xray down, power and always-on proxy alike, until a client turns
	// masir on: a daemon the CLI started for other work (subscriptions, rules,
	// updates) shouldn't start proxying.
	idle atomic.Bool

	stopOnce sync.Once
	stopped  chan struct{}
}

type running struct {
	proxy     bool
	proxyAddr string
	tun       bool
	tunName   string // kept while the TUN runs: Xray can't rename one
}

func New(xc *xray.Core, id identity.Identity) *Server {
	if xc == nil {
		panic("server.New: xray core is nil")
	}
	s := &Server{
		xray:      xc,
		id:        id,
		startedAt: time.Now(),
		state:     store.State{Settings: store.DefaultSettings()},
		logs:      newHub(),
		changes:   newSignal(),
		tests:     map[string]context.CancelFunc{},
		stopped:   make(chan struct{}),
	}
	xray.SetLogSink(s.Log)
	return s
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
	m.SetInterval(time.Duration(st.Settings.SubIntervalMinutes) * time.Minute)
	s.subs = m
	s.refreshSubscriptionCounts()

	// An idle daemon is one a short command started for its own work. It leaves Xray
	// down, but it must not change what the person chose: the saved power and tunnel
	// state come back when a daemon that isn't idle starts. status() reports power as
	// off while idle, so a UI doesn't show "Connecting..." for a daemon that may never
	// start Xray.
	s.initUpdates(dir)
	s.mu.Lock()
	s.dir, s.state = dir, st
	s.analytics = analytics.New(analytics.Options{
		Dir:     dir,
		Version: Version,
		Enabled: st.Settings.Analytics,
		Proxy:   s.xray,
	})
	if err := s.reconcileLocked(ctx); err != nil {
		log.Printf("start xray: %v", err)
	}
	s.mu.Unlock()
	s.track("daemon_started", map[string]any{
		"idle":       s.idle.Load(),
		"privileged": s.id.Privileged,
	})

	go m.Run(ctx)
	go s.retryFailed(ctx)
	go s.maintainUpdates(ctx)
	go s.analyticsLoop(ctx)
	return nil
}

// StartIdle makes Boot leave Xray down and masir off, until SetPower turns it on or
// always-on proxy is switched on. Call it before Boot.
func (s *Server) StartIdle() { s.idle.Store(true) }

// Idle reports whether the daemon is still idle.
func (s *Server) Idle() bool { return s.idle.Load() }

// Stopped is closed when a client asks the daemon to shut down.
func (s *Server) Stopped() <-chan struct{} { return s.stopped }

// Restart ends the daemon so it starts the binary on disk in its place, as after a
// deferred update.
func (s *Server) Restart(_ context.Context, _ *pb.RestartRequest) (*pb.RestartResponse, error) {
	s.Log("info", "restart requested")
	s.restartForUpdate()
	return &pb.RestartResponse{}, nil
}

func (s *Server) Shutdown(ctx context.Context, _ *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	s.Log("info", "shutdown requested")
	s.TrackDaemonStopped("client")
	// Tear the TUN and proxy down before answering. A new daemon waits for this reply
	// before taking the socket, so returning first would let it start while the old TUN
	// still held its address.
	_ = s.xray.Close()
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
