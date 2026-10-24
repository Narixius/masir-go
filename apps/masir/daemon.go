package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
	"github.com/narixius/masir-go/libs/masir-core/transport"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// installDir holds the root service's own copy of masird: a root daemon must not run a
// binary the user (or anything running as them) can replace.
const installDir = "/usr/local/lib/masir"

var errNoService = errors.New("installing masird as a service isn't supported on this OS yet")

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
	if err := ensureDaemon(sock); err != nil {
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
		s := ui.Spin("Restarting the masir service (sudo may ask for your password)")
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
	uid := os.Getuid()
	if v := a.str("user"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fail("--user must be a uid")
		}
		uid = n
	}
	return enableService(uid)
}

func daemonDisable(args) error {
	if os.Geteuid() != 0 {
		return asRoot("daemon", "disable")
	}
	if err := uninstallService(); err != nil {
		return err
	}
	if err := os.RemoveAll(installDir); err != nil {
		return err
	}
	ui.Success("masird service removed. It runs in the background again when masir needs it.")
	return nil
}

// daemonRun runs masird in the foreground, attached to this terminal.
func daemonRun(a args) error {
	bin, err := siblingBinary("masird")
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, a.pos...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// ensureDaemon starts masird in the background, as this user, unless something already
// answers on sock (a background daemon or the root service).
func ensureDaemon(sock string) error {
	if transport.Alive(sock) {
		return nil
	}
	bin, err := siblingBinary("masird")
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
	cmd := exec.Command(bin)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start masird: %w", err)
	}
	_ = cmd.Process.Release()
	return waitFor(func() bool { return transport.Alive(sock) }, 10*time.Second, "masird didn't start; see "+logf.Name())
}

// enableService installs masird as a root service serving uid. Run as the user, it
// re-runs itself under sudo, which asks for the password (or Touch ID).
func enableService(uid int) error {
	if os.Geteuid() != 0 {
		return asRoot("daemon", "enable", "--user", strconv.Itoa(os.Getuid()))
	}
	if uid == 0 {
		return fail("run `masir daemon enable` as your normal user; it asks for sudo itself")
	}
	if service.DefinitionPath() == "" {
		return errNoService
	}
	src, err := siblingBinary("masird")
	if err != nil {
		return err
	}
	bin, err := installBinary(src)
	if err != nil {
		return err
	}
	if err := installService(bin, uid); err != nil {
		return err
	}
	ui.Success("masird is installed as a root service. It starts at boot, and TUN mode needs no password from now on.")
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

// ensurePrivileged makes sure a root daemon is answering, as TUN needs: if the one
// running isn't root, it installs the service (sudo asks once) and waits for it to
// take over from the background daemon.
func ensurePrivileged() error {
	st, err := currentStatus()
	if err != nil {
		return err
	}
	if st.Privileged {
		return nil
	}
	if service.DefinitionPath() == "" {
		return fail("TUN mode needs a root service, which masir can't install on this OS yet; use `masir connect --proxy`")
	}
	ui.Note("TUN mode routes all system traffic, which needs root.\n"+
		"masir will install its daemon as a system service; sudo asks for your password once.",
		ui.Yellow("▲ Elevated Privileges Required"))
	if err := enableService(os.Getuid()); err != nil {
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

// installBinary copies masird and the geo assets beside it into installDir, root-owned.
func installBinary(src string) (string, error) {
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return "", err
	}
	bin := filepath.Join(installDir, "masird")
	if err := copyFile(src, bin, 0o755); err != nil {
		return "", err
	}
	for _, dat := range []string{"geoip.dat", "geosite.dat"} {
		from := filepath.Join(filepath.Dir(src), dat)
		if _, err := os.Stat(from); err == nil {
			if err := copyFile(from, filepath.Join(installDir, dat), 0o644); err != nil {
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

// siblingBinary finds name next to this executable, where masir and masird ship together.
func siblingBinary(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%s not found next to %s", name, exe)
	}
	return p, nil
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
