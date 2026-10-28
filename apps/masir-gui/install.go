package main

// Installing what the app can give the system: the daemon as a background service, and
// masir as a command-line tool. Both run off the main thread and report by toast.

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// readService asks the OS service manager. Tests replace it so they never look at the
// machine they run on.
var readService = func() service.Info { return service.Status() }

// serviceDefined asks whether a service definition exists. It is the authority the daemon
// watch trusts: a status from a daemon that is being removed still says a service exists,
// and trusting it would start no daemon and warn about a service that is already gone.
// Tests replace it for the same reason as readService.
var serviceDefined = service.Installed

// refreshServiceState keeps the service badge honest when no daemon is running to report
// it. It runs off the main thread and at most every few seconds.
func (a *app) refreshServiceState() {
	if !a.serviceChecked.IsZero() && time.Since(a.serviceChecked) < 5*time.Second {
		return
	}
	a.serviceChecked = time.Now()
	a.async(func() {
		info := readService()
		a.post(func() {
			if a.status == nil {
				a.status = &pb.Status{}
			}
			a.status.ServiceInstalled = info.Installed
			a.status.ServiceRunning = info.Running
			a.status.ServiceEnabled = info.Enabled
			if info.Installed != a.lastServiceInstalled {
				a.lastServiceInstalled = info.Installed
				a.binary = daemonBinary()
			}
		})
	})
}

// cliState is the command-line tool as Settings shows it: the last check, refreshed in the
// background, never with a shell or registry read in the frame that opens Settings.
func (a *app) cliState() cliStatus {
	if !a.cliChecked {
		a.refreshCLIState()
	}
	return a.cli
}

// cliInstallFile records what the automatic install did: done after a successful one,
// off when the person removed the command, so a later launch does not put it back.
func cliInstallFile() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "cli-install")
}

func (a *app) cliChoice() string {
	if a.cliFile == "" {
		return ""
	}
	b, err := os.ReadFile(a.cliFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (a *app) setCLIChoice(v string) {
	if a.cliFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.cliFile), 0o700); err != nil {
		log.Printf("cli: %v", err)
		return
	}
	if err := os.WriteFile(a.cliFile, []byte(v+"\n"), 0o600); err != nil {
		log.Printf("cli: %v", err)
	}
}

// stableInstallLocation keeps the automatic install out of a disk image, a translocated
// copy, or a temporary extraction: a link into one of those would break as soon as it
// went away. MASIR_CLI_INSTALL=1 forces it for development.
func stableInstallLocation(bin string) bool {
	if os.Getenv("MASIR_CLI_INSTALL") == "1" {
		return true
	}
	p, err := filepath.Abs(bin)
	if err != nil {
		return false
	}
	if strings.Contains(p, string(filepath.Separator)+"AppTranslocation"+string(filepath.Separator)) {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		if strings.HasPrefix(p, "/Applications/") {
			return true
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return strings.HasPrefix(p, filepath.Join(home, "Applications")+string(filepath.Separator))
		}
		return false
	case "windows":
		low := strings.ToLower(p)
		return !strings.Contains(low, `\appdata\local\temp\`) && !strings.HasPrefix(p, `\\`)
	default:
		for _, prefix := range []string{"/tmp/", "/var/tmp/", "/run/", "/media/", "/mnt/", "/Volumes/"} {
			if strings.HasPrefix(p, prefix) {
				return false
			}
		}
		return true
	}
}

// autoInstallCLI installs masir on first launch without asking. It is silent on success
// and on failure: Settings still shows the state and offers the manual install button.
func (a *app) autoInstallCLI() {
	a.cliChecked = true
	bin := a.cliBinary()
	force := os.Getenv("MASIR_CLI_INSTALL") == "1"
	skip := bin == "" ||
		os.Getenv("MASIR_NO_CLI_INSTALL") != "" ||
		(!force && os.Getenv("MASIR_APP_NAME") != "" && os.Getenv("MASIR_APP_NAME") != "Masir") ||
		!stableInstallLocation(bin)
	if skip {
		a.refreshCLIState()
		return
	}
	// The command is part of the app, not an optional extra: Settings no longer offers to
	// remove it, and an opt-out left by an older version is not honored.
	choice := a.cliChoice()
	done := choice == "done"
	a.cliBusy = true
	a.async(func() {
		if done {
			repairCLI(bin)
		}
		var st cliStatus
		var err error
		if done {
			st = cliStatusNow(bin)
		} else {
			st, err = installCLI(bin)
		}
		if err != nil {
			log.Printf("cli: %v", err)
		}
		a.post(func() {
			a.cliBusy = false
			a.cli, a.cliChecked = st, true
			if err == nil && st.installed && st.ours && !done {
				a.setCLIChoice("done")
			}
		})
	})
}

// refreshCLIState checks where the command-line tool is, off the main thread.
func (a *app) refreshCLIState() {
	a.cliChecked = true
	bin := a.cliBinary()
	a.async(func() {
		st := cliStatusNow(bin)
		a.post(func() {
			a.cli, a.cliChecked = st, true
		})
	})
}

// cliBinary is the masir to install: the one the app ships with, even when another is on
// the PATH.
func (a *app) cliBinary() string {
	if p := bundledBinary(); p != "" {
		return p
	}
	return a.binary
}

func (a *app) installCommandLine() {
	if a.cliBusy {
		return
	}
	bin := a.cliBinary()
	if bin == "" {
		a.notify("This app ships no masir executable to install")
		return
	}
	a.cliBusy = true
	a.async(func() {
		st, err := installCLI(bin)
		a.post(func() {
			a.cliBusy = false
			if err != nil {
				a.fail("Couldn't install the command line tool: ", err)
				return
			}
			a.cli, a.cliChecked = st, true
			if st.installed && st.ours {
				a.setCLIChoice("done")
			}
			switch {
			case st.installed && !st.ours:
				a.notify("masir is already installed at " + st.path)
			case st.needsShell:
				a.notify("masir is installed; open a new terminal to use it")
			case st.installed && st.onPath:
				a.notify("masir is installed and on your PATH")
			case st.installed:
				a.notify("masir is installed in " + st.dir + "; add it to your PATH to use it")
			default:
				a.notify("masir was not installed; see the log for why")
			}
		})
	})
}

func (a *app) installDaemonService() {
	if a.serviceBusy {
		return
	}
	if a.binary == "" {
		a.notify("This app ships no masir executable to install the service from")
		return
	}
	a.serviceBusy = true
	bin := a.binary
	a.async(func() {
		err := installService(bin)
		a.post(func() {
			a.serviceBusy = false
			if err != nil {
				a.fail("Couldn't install the background service: ", err)
				return
			}
			a.binary = daemonBinary() // the service's copy is the one to start from now
			a.serviceChecked = time.Time{}
			a.refreshServiceState()
			a.notify("The background service is installed")
		})
	})
}

func (a *app) startDaemonService() {
	if a.serviceBusy {
		return
	}
	bin := a.binary
	if bin == "" {
		a.notify("This app ships no masir executable to start the service with")
		return
	}
	a.serviceBusy = true
	a.async(func() {
		err := startService(bin)
		a.post(func() {
			a.serviceBusy = false
			if err != nil {
				a.fail("Couldn't start the background service: ", err)
				return
			}
			a.serviceChecked = time.Time{}
			a.refreshServiceState()
			a.notify("The background service is starting")
		})
	})
}

func (a *app) removeDaemonService() {
	if a.serviceBusy {
		return
	}
	if a.binary == "" {
		a.notify("This app ships no masir executable to remove the service with")
		return
	}
	a.serviceBusy = true
	bin := a.binary
	a.async(func() {
		err := uninstallService(bin)
		a.post(func() {
			a.serviceBusy = false
			if err != nil {
				a.fail("Couldn't remove the background service: ", err)
				return
			}
			// The service's directory is gone; the bundle's masir is the daemon now.
			a.binary = daemonBinary()
			a.serviceChecked = time.Time{}
			a.refreshServiceState()
			a.notify("The background service is removed")
		})
	})
}
