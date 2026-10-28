package main

import (
	"testing"

	"github.com/narixius/masir-go/libs/masir-core/service"
)

// A service being installed, removed or started takes its daemon down for a moment. The
// watch must wait: starting a second daemon there would fight the service, and warning
// would point the person at a service that is on its way.
func TestDaemonStepWaitsWhileAServiceActionRuns(t *testing.T) {
	a := newTestApp(ukServers())
	a.serviceBusy = true
	serviceDefined = func() bool { return true }
	if got := a.daemonStep(); got != daemonWait {
		t.Fatalf("step = %v, want daemonWait", got)
	}
}

// A stopped service is still the supervisor: the watch leaves it alone and connect
// starts it, instead of starting a daemon of its own or sending the person to Settings.
func TestDaemonStepLeavesAStoppedServiceToConnect(t *testing.T) {
	a := newTestApp(ukServers())
	serviceDefined = func() bool { return true }
	if got := a.daemonStep(); got != daemonLeaveToService {
		t.Fatalf("step = %v, want daemonLeaveToService", got)
	}
}

func TestDaemonStepStartsADaemonWithoutAService(t *testing.T) {
	a := newTestApp(ukServers())
	if got := a.daemonStep(); got != daemonStart {
		t.Fatalf("step = %v, want daemonStart", got)
	}
}

// A status from a daemon that was just removed still says a service exists. The watch
// must not take that for one: the definition is what counts.
func TestDaemonStepIgnoresAStaleServiceStatus(t *testing.T) {
	a := newTestApp(ukServers())
	a.status.ServiceInstalled = true
	serviceDefined = func() bool { return false }
	if got := a.daemonStep(); got != daemonStart {
		t.Fatalf("step = %v, want daemonStart", got)
	}
}

func TestDaemonStepWaitsDuringAnUpdate(t *testing.T) {
	a := newTestApp(ukServers())
	a.upd.phase = updateDownloading
	if got := a.daemonStep(); got != daemonWait {
		t.Fatalf("step = %v, want daemonWait", got)
	}
}

// Connecting with a stopped service starts the service (one permission prompt) rather
// than a second, unsupervised root daemon.
func TestConnectStartsAStoppedServiceInsteadOfARootDaemon(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	daemonAlive = func() bool { return false } // nothing answers while the service is stopped
	readService = func() service.Info { return service.Info{Installed: true, Running: false, Enabled: true} }
	serviceDefined = func() bool { return true }

	var serviceStarts int
	startServiceNow = func(string) error { serviceStarts++; return nil }
	rootStarts := 0
	startRootDaemon = func(string) error { rootStarts++; return nil }

	if err := a.power(true); err != nil {
		t.Fatal(err)
	}
	if serviceStarts != 1 {
		t.Fatalf("service starts = %d, want 1", serviceStarts)
	}
	if rootStarts != 0 {
		t.Fatalf("started %d root daemons beside the service, want 0", rootStarts)
	}
	if !f.called("power on") || !f.called("tun") {
		t.Fatalf("connect didn't reach the daemon: %v", f.calls)
	}
}

// Without a service, connecting runs the daemon as root for this session, which is all
// tunnel mode needs.
func TestConnectRunsARootDaemonWhenNoServiceIsInstalled(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	daemonAlive = func() bool { return false } // nothing answers yet
	readService = func() service.Info { return service.Info{} }

	rootStarts := 0
	startRootDaemon = func(string) error { rootStarts++; return nil }
	serviceStarts := 0
	startServiceNow = func(string) error { serviceStarts++; return nil }

	if err := a.power(true); err != nil {
		t.Fatal(err)
	}
	if rootStarts != 1 {
		t.Fatalf("root starts = %d, want 1", rootStarts)
	}
	if serviceStarts != 0 {
		t.Fatalf("started a service that isn't installed")
	}
	if !f.called("power on") || !f.called("tun") {
		t.Fatalf("connect didn't reach the daemon: %v", f.calls)
	}
}

// The panel says why nothing answers, and the switch stays usable so the stopped service
// can be started from it.
func TestConnectIsAvailableWhileTheServiceIsStopped(t *testing.T) {
	f := ukServers()
	f.status.ServiceInstalled = true
	f.status.ServiceRunning = false
	f.status.ServiceEnabled = true
	a := newTestApp(f)
	a.reachable = false // the panel has nothing to show yet
	daemonAlive = func() bool { return false }
	readService = func() service.Info { return service.Info{Installed: true, Running: false, Enabled: true} }
	serviceDefined = func() bool { return true }

	tt := tester(a)
	if !tt.HasText("The background service is stopped") {
		t.Fatalf("no stopped hint: %q", tt.Texts())
	}
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	if !f.called("power on") {
		t.Fatalf("connect was disabled while the service was stopped")
	}
}
