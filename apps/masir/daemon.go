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
	"strconv"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/client"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
	masird "github.com/narixius/masir-go/libs/masir-daemon"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

var errNoService = errors.New("installing the masir daemon as a service isn't supported on this OS yet")

func daemonStart(args) error {
	if service.Installed() { // the service is the supervisor; start it, not a second daemon
		s := ui.Spin("Starting the masir service (" + elevationHint + ")")
		if err := restartService(); err != nil {
			s.Fail(err.Error())
			return errReported
		}
		s.Stop("The masir service is running.")
		return nil
	}
	sock, err := paths.SocketPath()
	if err != nil {
		return err
	}
	if daemonAlive() {
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
		if err := waitFor(func() bool { return !daemonAlive() }, 10*time.Second, "the daemon didn't stop"); err != nil {
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

// daemonRollback restores the service's previous binary after an update that installed
// but didn't start. `masir update` keeps it beside the service's copy.
func daemonRollback(args) error {
	if !service.Installed() {
		return fail("no service is installed, so there is no previous service version to restore")
	}
	if !elevated() {
		return elevate("daemon", "rollback")
	}
	prev := service.BinaryPath() + ".previous"
	if _, err := os.Stat(prev); err != nil {
		return fail("there is no previous version to restore")
	}
	if err := copyFile(prev, service.BinaryPath(), 0o755); err != nil {
		return err
	}
	if same, err := sameContent(prev, service.BinaryPath()); err != nil || !same {
		return fail("the restored copy doesn't match the saved version")
	}
	// The restore rewrote the binary, so the SELinux label of the fresh copy applies.
	if err := labelServiceBinary(service.BinaryPath()); err != nil {
		return err
	}
	if err := restartService(); err != nil {
		return err
	}
	ui.Success("The previous masir version is running.")
	return nil
}

func daemonDisable(args) error {
	if !elevated() {
		return elevate("daemon", "disable")
	}
	if err := uninstallService(); err != nil {
		return err
	}
	if err := os.RemoveAll(service.InstallDir()); err != nil {
		return err
	}
	ui.Success("The masir service is removed. The daemon runs in the background again when masir needs it.")
	return nil
}

// daemonCheck answers a running daemon that is about to install this version: stdin
// holds the user's rules and where the geo data is, stdout gets the rules this version
// can't build. It changes nothing.
//
// The daemon runs it with pipes. Typed at a terminal it would wait on stdin for ever, which
// looks like a hang, so it says what it is instead.
func daemonCheck(args) error {
	if ui.Interactive() {
		return advised("This is an internal command: the daemon runs it on a new version before installing it.",
			"To check your own rules, run:", "masir rules check")
	}
	return masird.CheckRules(os.Stdin, os.Stdout)
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
	bin, err := self()
	if err != nil {
		return err
	}
	return client.Start(appCtx, bin, idle)
}

// enableService installs the daemon as a privileged service serving user (a uid, or a
// SID on Windows). Run as the user, it re-runs itself with privileges: sudo asks for
// the password, or Windows for permission.
func enableService(user string) error {
	if !elevated() {
		// Ask and stop here, as the person: after sudo the daemon is found by the uid it
		// serves, not by this process's own sockets.
		if err := stopBeforeInstall(currentUser()); err != nil {
			return err
		}
		return elevate("daemon", "enable", "--user", currentUser())
	}
	if user == "" || user == "0" {
		return advised("This needs your normal user, not root: masir asks for permission itself.", "Run it again without sudo:", "masir daemon enable")
	}
	if !service.Supported() {
		return errNoService
	}
	if err := stopBeforeInstall(user); err != nil {
		return err
	}
	src, err := self()
	if err != nil {
		return err
	}
	if _, err := installBinary(src); err != nil {
		return err
	}
	if err := installService(service.BinaryPath(), user); err != nil {
		return err
	}
	ui.Success("masir is installed as a system service. It starts at boot, and TUN mode needs no permission prompt from now on.")
	return nil
}

// stopBeforeInstall asks before taking down the daemon that would hold the install up,
// then stops it over the socket. The service manager's own stop (launchd's bootout,
// systemd's stop) sends a signal, and that waits on the streams a client keeps open —
// the app holds two — so asking over the socket stops it at once.
//
// There is a prompt only in a terminal: the app's Install Service is the person's answer
// already, and its pkexec'd masir has no terminal to ask on.
func stopBeforeInstall(user string) error {
	sock, ok := socketForInstall(user)
	if !ok {
		return nil
	}
	stop, err := ui.Confirm("A masir daemon is already running. Stop it and install the service?", true)
	if err != nil {
		return err
	}
	if !stop {
		return advised("Nothing was installed: a running daemon owns the socket, and the service can't take it over while it does.",
			"Install it when you're ready:", "masir daemon enable")
	}
	s := ui.Spin("Stopping the masir daemon")
	if err := stopTheDaemon(sock); err != nil {
		s.Fail(err.Error())
		return errReported
	}
	s.Stop("Daemon stopped.")
	return nil
}

// socketForInstall is where the daemon the install must stop answers. As the person that
// is the live socket; as root it is the service's socket for the uid it serves, which is
// why the lookup takes the uid rather than trusting root's own.
func socketForInstall(user string) (string, bool) {
	if sock, ok := liveDaemonSocket(); ok {
		return sock, true
	}
	if !elevated() {
		return "", false
	}
	uid, err := strconv.Atoi(user)
	if err != nil {
		return "", false
	}
	sock := paths.PrivilegedSocketPath(uid)
	return sock, client.AliveAt(sock)
}

// stopDaemonAt asks the daemon on sock to exit and waits until it has let go of it.
func stopDaemonAt(sock string) error {
	conn, c, err := client.DialSocket(sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(appCtx, 5*time.Second)
	defer cancel()
	// A daemon that exits before answering is stopped too; the socket is what counts.
	_, _ = c.Shutdown(ctx, &pb.ShutdownRequest{})
	return waitFor(func() bool { return !client.AliveAt(sock) }, 10*time.Second, "the daemon didn't stop")
}

// liveDaemonSocket and stopTheDaemon are the machine calls an install makes. Tests
// replace them so they never stop a daemon on the machine they run on.
var (
	liveDaemonSocket = client.LiveSocket
	stopTheDaemon    = stopDaemonAt
)

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

// ensurePrivileged makes sure a privileged daemon is answering, as TUN needs. If the
// one running isn't, it starts the daemon as root (Administrator on Windows) for this
// session, which takes over from the background daemon. It installs nothing: `masir
// daemon enable` is the user's choice, offered once it worked.
func ensurePrivileged() error {
	st, err := currentStatus()
	if err != nil {
		return err
	}
	if st.Privileged {
		return nil
	}
	if !canElevateSilently() { // only warn when a prompt is actually coming
		ui.Note("TUN mode routes all system traffic, which needs "+privilegeName+".\n"+elevationNote,
			ui.Yellow("▲ Elevated Privileges Required"))
	}
	dir, err := paths.ConfigDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "masird.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = startElevatedDaemon(currentUser(), logf)
	logf.Close()
	if err != nil {
		return err
	}

	cachedClient = nil // the privileged daemon takes over the socket
	s := ui.Spin("Starting the daemon with " + privilegeName)
	err = waitFor(func() bool {
		c, ok := runningDaemon()
		if !ok {
			return false
		}
		ctx, cancel := context.WithTimeout(appCtx, time.Second)
		defer cancel()
		res, err := c.GetStatus(ctx, &pb.GetStatusRequest{})
		return err == nil && res.Status.Privileged
	}, 30*time.Second, "the privileged daemon didn't start; see "+logPath)
	if err != nil {
		s.Fail(err.Error())
		return errReported
	}
	s.Stop("The daemon runs with " + privilegeName + ".")
	if service.Supported() && !service.Installed() {
		ui.Info(enableTip)
	}
	return nil
}

// installBinary copies this executable and the geo data beside it into service.InstallDir(),
// which only root or Administrators can change: the service must never run a binary
// the user can modify. (On Windows the service writes the Wintun driver it embeds.)
func installBinary(src string) (string, error) {
	dir := service.InstallDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stopServiceForInstall()
	name := filepath.Base(service.BinaryPath())
	bin := filepath.Join(dir, name)
	if err := copyFile(src, bin, 0o755); err != nil {
		return "", err
	}
	// The service runs this copy as root. A short write or a swapped source must not
	// become the daemon.
	if same, err := sameContent(src, bin); err != nil || !same {
		return "", errors.New("the service's copy of masir doesn't match the running binary")
	}
	os.Remove(filepath.Join(dir, "masird")) // the separate daemon binary of earlier versions
	if runtime.GOOS == "darwin" {
		// An older app wrapper is not part of the service now, and a stale wrapper
		// would keep the wrong name and icon in Login Items. Do not try to remove a
		// lowercase "masir": on the default case-insensitive APFS it is this same file.
		os.RemoveAll(filepath.Join(dir, "Masir.app"))
	}
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		from := filepath.Join(filepath.Dir(src), f)
		if _, err := os.Stat(from); err == nil {
			to := filepath.Join(dir, f)
			if err := copyFile(from, to, 0o644); err != nil {
				return "", err
			}
			if same, err := sameContent(from, to); err != nil || !same {
				return "", errors.New("the service's copy of " + f + " doesn't match the running data")
			}
		}
	}
	// Give the service binary the app's Finder icon, so Login Items shows Masir's icon
	// instead of the generic executable. The files sit beside it; updates re-apply them.
	for _, f := range []string{"AppIcon.rsrc", "FinderInfo.bin"} {
		to := filepath.Join(dir, f)
		for _, from := range []string{
			filepath.Join(filepath.Dir(src), "..", f),
			filepath.Join(filepath.Dir(src), f),
			to,
		} {
			if filepath.Clean(from) == filepath.Clean(to) {
				break
			}
			if fi, err := os.Stat(from); err == nil && !fi.IsDir() {
				if err := copyFile(from, to, 0o644); err != nil {
					return "", err
				}
				break
			}
		}
	}
	applyFileIcon(bin, dir)
	if err := labelServiceBinary(bin); err != nil {
		return "", err
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
	preserveOwner(tmp, to)
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

// sudo runs argv as root, attached to the terminal for the password prompt. When the
// command fails, it has already said why (as have sudo and launchctl), so the error
// reports nothing more.
func sudo(argv ...string) error {
	cmd := exec.Command("sudo", append([]string{"--"}, argv...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return errReported
		}
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
	return client.WaitFor(appCtx, ok, timeout, failure)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
