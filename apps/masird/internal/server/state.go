package server

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"

	"github.com/narixius/masir-go/apps/masird/internal/atomicfile"
	"github.com/narixius/masir-go/apps/masird/internal/compose"
	"github.com/narixius/masir-go/apps/masird/internal/store"
	"github.com/narixius/masir-go/apps/masird/internal/subs"
	"github.com/narixius/masir-go/apps/masird/internal/xray"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// change edits a copy of the state, applies it, and saves it only once it runs.
func (s *Server) change(ctx context.Context, msg string, fn func(*store.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.state.Clone()
	if err := fn(&next); err != nil {
		return toStatus(err)
	}
	if err := s.applyLocked(ctx, next); err != nil {
		return err
	}
	if err := store.Save(s.dir, next); err != nil {
		_ = s.applyLocked(context.WithoutCancel(ctx), s.state) // keep what runs and what's saved in agreement
		return status.Error(codes.Internal, "save state: "+err.Error())
	}
	s.state = next
	if msg != "" {
		s.Log("info", msg)
	}
	return nil
}

// applyLocked composes st with the current selection and brings Xray to it:
// running when power or always-on proxy is on (and the daemon isn't idle), stopped
// otherwise. Expects s.mu held.
func (s *Server) applyLocked(ctx context.Context, st store.State) error {
	active, err := s.subs.Active()
	if err != nil {
		return status.Error(codes.Internal, "read selection: "+err.Error())
	}
	runs := st.Settings.XrayRuns() && !s.idle.Load()
	tun := runs && st.Settings.Power && st.Settings.Tun && s.id.Privileged
	in := compose.Input{
		State:   st,
		Proxy:   runs,
		Tun:     tun,
		Active:  active.Outbound,
		Resolve: s.resolveTarget,
	}
	if tun {
		in.TunName = compose.TunName()
	}
	out, err := compose.Build(in)
	if err != nil {
		return status.Error(codes.Internal, "compose: "+err.Error())
	}
	p, err := xray.ParseProfile(out.Config)
	if err != nil {
		return status.Error(codes.Internal, "compose: "+err.Error())
	}

	if runs {
		if err := s.xray.Apply(ctx, p); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	} else if err := s.xray.Close(); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	s.resolved = out.Rules
	s.running = running{proxy: runs, tun: tun}
	if runs {
		host := "127.0.0.1"
		if st.Settings.AllowLAN {
			host = "0.0.0.0"
		}
		s.running.proxyAddr = net.JoinHostPort(host, strconv.Itoa(st.Settings.ProxyPort))
	}
	defer s.changes.notify()

	// xray.json is output: what runs (or would), for people to read. Never read back.
	if raw, err := p.Marshal(); err == nil {
		s.config = raw
		if err := atomicfile.Write(filepath.Join(s.dir, "xray.json"), raw); err != nil {
			s.Log("warn", "write xray.json: "+err.Error())
		}
	}
	return nil
}

// reconcileLocked applies the saved state when no caller is waiting on the result (boot,
// subscription refreshes, retries). A failure is kept in lastErr for the status and the
// retry loop, since nothing else would report it. Expects s.mu held.
func (s *Server) reconcileLocked(ctx context.Context) error {
	err := s.applyLocked(ctx, s.state)
	prev := s.lastErr
	s.lastErr = ""
	if err != nil {
		s.lastErr = status.Convert(err).Message()
		if s.lastErr != prev {
			s.Log("error", "apply config: "+s.lastErr)
		}
	} else if prev != "" {
		s.Log("info", "config applied after earlier failure")
	}
	if s.lastErr != prev {
		s.changes.notify()
	}
	return err
}

// subscriptionsChanged rebuilds after a refresh or removal: pinned configs may have
// moved, fallen back to proxy, or the selection may have followed a rotated config.
func (s *Server) subscriptionsChanged(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reconcileLocked(ctx)
}

func (s *Server) resolveTarget(t store.Target) (subs.Config, bool) {
	return s.subs.Find(t.SubscriptionID, t.ConfigID, t.Name)
}

func toStatus(err error) error {
	if _, ok := status.FromError(err); ok {
		return err // already a gRPC status
	}
	switch {
	case errors.Is(err, store.ErrInvalid), errors.Is(err, subs.ErrInvalid), errors.Is(err, xray.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, subs.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrExists), errors.Is(err, subs.ErrExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, store.ErrInUse), errors.Is(err, xray.ErrNotRunning):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, subs.ErrFetch):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
