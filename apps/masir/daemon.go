package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
	"github.com/narixius/masir-go/libs/masir-core/transport"
	masird "github.com/narixius/masir-go/libs/masir-daemon"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

var errNoService = errors.New("installing the masir daemon as a service isn't supported on this OS yet")

func daemonStart(args) error {
	sock, err := paths.SocketPath()
	if err != nil {
		return err
	}
	if transport.Alive(sock) {
		ui.Info("The masir daemon is already running.")
		return nil
	}
	s := ui.Spin("Starting the masir daemon")
	if err := ensureDaemon(sock, false); err != nil {
		s.Fail(err.Error())
		return errReported
	}
	s.Stop("Daemon started in the background.")
	return nil
}

func daemonStop(args) error {
	c, ok := runningDaemon()
	if !ok {
		ui.Info("The masir daemon isn't running.")
		return nil
	}
	ctx, cancel := rpc()
	defer cancel()
	if _, err := c.Shutdown(ctx, &pb.ShutdownRequest{}); err != nil {
		return err
	}
	ui.Success("Daemon stopped. Proxy and TUN are off until it starts again.")
	return nil
}

func daemonRestart(a args) error {
	if service.Installed() {
		s := ui.Spin("Restarting the masir service (" + elevationHint + ")")
		if err := restartService(); err != nil {
			s.Fail(err.Error())
			return errReported
		}
		s.Stop("Service restarted.")
		return nil
	}
	if c, ok := runningDaemon(); ok {
		ctx, cancel := rpc()
		_, err := c.Shutdown(ctx, &pb.ShutdownRequest{})
		cancel()
		if err != nil {
			return err
		}
		sock, _ := paths.SocketPath()
		if err := waitFor(func() bool { return !transport.Alive(sock) }, 10*time.Second, "the daemon didn't stop"); err != nil {
			return err
		}
	}
	return daemonStart(a)
}

func daemonEnable(a args) error {
	user := a.str("user")
	if user == "" {
		user = currentUser()
	}
	return enableService(user)
}

func daemonDisable(args) error {
	if !elevated() {
		return elevate("daemon", "disable")
	}
	if err := uninstallService(); err != nil {
		return err
	}
	if err := os.RemoveAll(installDir()); err != nil {
		return err
	}
	ui.Success("The masir service is removed. The daemon runs in the background again when masir needs it.")
	return nil
}

// daemonRun runs the daemon in this process, in the foreground: it's how the background
// daemon and the root service run (both start this same executable).
func daemonRun(a args) error {
	return runDaemon(masird.Options{User: a.str("user"), Idle: a.boolean("idle", false), Version: version})
}

// ensureDaemon starts the daemon in the background, as this user, unless something already
// answers on sock (a background daemon or the root service). An idle
// daemon keeps Xray down until masir is turned on: commands that only need the daemon
// for subscriptions, rules or updates don't start proxying. `masir daemon start`
// starts it normally, following the saved settings.
func ensureDaemon(sock string, idle bool) error {
	if transport.Alive(sock) {
		return nil
	}
	bin, err := self()
	if err != nil {
		return err
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "masird.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	args := []string{"daemon", "run"}
	if idle {
		args = append(args, "--idle")
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the masir daemon: %w", err)
	}
	_ = cmd.Process.Release()
	return waitFor(func() bool { return transport.Alive(sock) }, 10*time.Second, "the masir daemon didn't start; see "+logf.Name())
}

// enableService installs the daemon as a privileged service serving user (a uid, or a
// SID on Windows). Run as the user, it re-runs itself with privileges: sudo asks for
// the password, or Windows for permission.
func enableService(user string) error {
	if !elevated() {
		return elevate("daemon", "enable", "--user", currentUser())
	}
	if user == "" || user == "0" {
		return fail("run `masir daemon enable` as your normal user; it asks for permission itself")
	}
	if !service.Supported() {
		return errNoService
	}
	src, err := self()
	if err != nil {
		return err
	}
	bin, err := installBinary(src)
	if err != nil {
		return err
	}
	if err := installService(bin, user); err != nil {
		return err
	}
	ui.Success("masir is installed as a system service. It starts at boot, and TUN mode needs no permission prompt from now on.")
	return nil
}

// enableTun turns TUN on, elevating the daemon first when it isn't root.
func enableTun() error {
	if err := ensurePrivileged(); err != nil {
		return err
	}
	_, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetTunEnabledResponse, error) {
		return c.SetTunEnabled(ctx, &pb.SetTunEnabledRequest{Enabled: true})
	})
	return err
}

// ensurePrivileged makes sure a privileged daemon is answering, as TUN needs: if the
// one running isn't, it installs the service (sudo or Windows asks once) and waits for
// it to take over from the background daemon.
func ensurePrivileged() error {
	st, err := currentStatus()
	if err != nil {
		return err
	}
	if st.Privileged {
		return nil
	}
	if !service.Supported() {
		return fail("TUN mode needs a system service, which masir can't install on this OS yet; use `masir connect --proxy`")
	}
	ui.Note("TUN mode routes all system traffic, which needs "+privilegeName+".\n"+elevationNote,
		ui.Yellow("▲ Elevated Privileges Required"))
	if err := enableService(currentUser()); err != nil {
		return err
	}
	cachedClient = nil // the root daemon has taken over the socket
	s := ui.Spin("Connecting to the privileged daemon")
	err = waitFor(func() bool {
		c, ok := runningDaemon()
		if !ok {
			return false
		}
		ctx, cancel := context.WithTimeout(appCtx, time.Second)
		defer cancel()
		res, err := c.GetStatus(ctx, &pb.GetStatusRequest{})
		return err == nil && res.Status.Privileged
	}, 30*time.Second, "the root service didn't come up; check /var/log/masird.log")
	if err != nil {
		s.Fail(err.Error())
		return errReported
	}
	s.Stop("Connected to the privileged daemon.")
	return nil
}

// installBinary copies this executable and the files that ship beside it (geo data,
// and wintun.dll on Windows) into installDir(), which only root or Administrators can
// change: the service must never run a binary the user can modify.
func installBinary(src string) (string, error) {
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stopServiceForInstall()
	name := "masir"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	if err := copyFile(src, bin, 0o755); err != nil {
		return "", err
	}
	os.Remove(filepath.Join(dir, "masird")) // the separate daemon binary of earlier versions
	for _, f := range []string{"geoip.dat", "geosite.dat", "wintun.dll"} {
		from := filepath.Join(filepath.Dir(src), f)
		if _, err := os.Stat(from); err == nil {
			if err := copyFile(from, filepath.Join(dir, f), 0o644); err != nil {
				return "", err
			}
		}
	}
	return bin, nil
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := to + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, to) // replacing in place keeps a running copy intact
}

// asRoot re-runs this binary under sudo with args, attached to the terminal for the prompt.
func asRoot(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return sudo(append([]string{exe}, args...)...)
}

func sudo(argv ...string) error {
	cmd := exec.Command("sudo", append([]string{"--"}, argv...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo %s: %w", filepath.Base(argv[0]), err)
	}
	return nil
}

// self is this executable, resolved through symlinks: masir is also the daemon, so the
// background daemon and the service run this same file.
func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func waitFor(ok func() bool, timeout time.Duration, failure string) error {
	deadline := time.Now().Add(timeout)
	for !ok() {
		if appCtx.Err() != nil {
			return appCtx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New(failure)
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
