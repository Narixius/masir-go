package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// cmdConnect turns masir on: TUN unless --proxy. It gets root first
// (TUN needs it), then turns everything on behind one spinner and says exactly what
// runs, so a TUN failure never hides behind an earlier "masir is on".
func cmdConnect(a args) error {
	tun := a.boolean("tun", true) && !a.boolean("proxy", false)
	if tun {
		if err := ensurePrivileged(); err != nil {
			return err
		}
	}
	sp := ui.Spin("Connecting")
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetPowerResponse, error) {
		return c.SetPower(ctx, &pb.SetPowerRequest{On: true})
	}); err != nil {
		sp.Fail("Couldn't turn masir on: " + message(err))
		return errReported
	}
	_, tunErr := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetTunEnabledResponse, error) {
		return c.SetTunEnabled(ctx, &pb.SetTunEnabledRequest{Enabled: tun})
	})
	st, err := currentStatus()
	if err != nil {
		sp.Fail(message(err))
		return errReported
	}
	switch {
	case tunErr != nil && cancelled(tunErr):
		return tunErr
	case tunErr != nil:
		sp.Fail("TUN couldn't start: " + message(tunErr))
		if st.ProxyRunning {
			ui.Warn(fmt.Sprintf("masir is on in proxy mode only: apps set to use %s go through it.", st.ProxyAddress))
		}
	case st.TunRunning:
		sp.Stop(fmt.Sprintf("Connected in TUN mode %s", ui.Dim("· proxy on "+st.ProxyAddress)))
	default:
		sp.Stop(fmt.Sprintf("Connected in proxy mode %s", ui.Dim("· proxy on "+st.ProxyAddress)))
	}

	switch {
	case st.ActiveConfigId == "":
		ui.Warn("No proxy selected yet, so everything goes direct. Pick one with `masir use`.")
	case st.Settings.GetFallback() == pb.Fallback_FALLBACK_DIRECT:
		ui.Info("Unmatched traffic goes direct. Send it through the proxy with `masir mode proxy`.")
	}
	if c, err := daemon(); err == nil {
		showConnectivity(c, st)
		fmt.Println()
	}
	if tunErr != nil {
		return errReported
	}
	return appCtx.Err()
}

func cmdDisconnect(args) error {
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetPowerResponse, error) {
		return c.SetPower(ctx, &pb.SetPowerRequest{On: false})
	}); err != nil {
		return err
	}
	st, err := currentStatus()
	if err != nil {
		return err
	}
	if st.ProxyRunning {
		ui.Success(fmt.Sprintf("Disconnected. TUN is off; the proxy on %s stays up (always-on proxy).", st.ProxyAddress))
	} else {
		ui.Success("Disconnected. The daemon keeps running; `masir connect` turns it back on.")
	}
	return nil
}

func cmdMode(a args) error {
	st, err := currentStatus()
	if err != nil {
		return err
	}
	current := "direct"
	if st.Settings.GetFallback() == pb.Fallback_FALLBACK_PROXY {
		current = "proxy"
	}
	mode := a.arg(0)
	if mode == "" {
		if !ui.Interactive() {
			fmt.Println(current)
			return nil
		}
		mode, err = ui.Select("Default routing mode (catch-all at the end of rules)", []ui.Option[string]{
			{Label: "Proxy", Hint: "route unmatched traffic through the active proxy", Value: "proxy"},
			{Label: "Direct", Hint: "send unmatched traffic directly without proxy", Value: "direct"},
		}, current)
		if err != nil {
			return err
		}
	}
	f := pb.Fallback_FALLBACK_DIRECT
	switch mode {
	case "proxy":
		f = pb.Fallback_FALLBACK_PROXY
	case "direct":
	default:
		return fail("mode must be proxy or direct")
	}
	_, err = call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetFallbackResponse, error) {
		return c.SetFallback(ctx, &pb.SetFallbackRequest{Fallback: f})
	})
	if status.Code(err) == codes.FailedPrecondition {
		return fail("%s; pick one with `masir use`", message(err))
	}
	if err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Routing mode set to %s (%s).", mode, appliedNote(st)))
	return nil
}

func currentStatus() (*pb.Status, error) {
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.GetStatusResponse, error) {
		return c.GetStatus(ctx, &pb.GetStatusRequest{})
	})
	if err != nil {
		return nil, err
	}
	return res.Status, nil
}

// appliedNote says whether a change already took effect.
func appliedNote(st *pb.Status) string {
	if st.XrayRunning {
		return "applied"
	}
	return "takes effect when you connect"
}
