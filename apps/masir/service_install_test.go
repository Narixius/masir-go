package main

import (
	"errors"
	"testing"
)

// The install stops a running daemon over the socket before it replaces the service, so
// the service manager never has to wait on the streams the app holds open. With nothing
// answering there is nothing to stop, and the install doesn't ask.
func TestInstallStopsARunningDaemon(t *testing.T) {
	oldSocket, oldStop := liveDaemonSocket, stopTheDaemon
	t.Cleanup(func() { liveDaemonSocket, stopTheDaemon = oldSocket, oldStop })

	var stopped []string
	stopTheDaemon = func(sock string) error {
		stopped = append(stopped, sock)
		return nil
	}

	liveDaemonSocket = func() (string, bool) { return "", false }
	if err := stopBeforeInstall("501"); err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 0 {
		t.Fatalf("stopped %v with nothing running", stopped)
	}

	// Tests have no terminal, so the confirmation takes its default: yes.
	liveDaemonSocket = func() (string, bool) { return "/tmp/daemon.sock", true }
	if err := stopBeforeInstall("501"); err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0] != "/tmp/daemon.sock" {
		t.Fatalf("stopped %v, want the live socket", stopped)
	}
}

// A daemon that won't stop must abort the install: installing over it is exactly what
// waits on the service manager's stop.
func TestInstallStopsWhenTheDaemonWont(t *testing.T) {
	oldSocket, oldStop := liveDaemonSocket, stopTheDaemon
	t.Cleanup(func() { liveDaemonSocket, stopTheDaemon = oldSocket, oldStop })

	liveDaemonSocket = func() (string, bool) { return "/tmp/daemon.sock", true }
	stopTheDaemon = func(string) error { return errors.New("the daemon didn't stop") }

	if err := stopBeforeInstall("501"); !errors.Is(err, errReported) {
		t.Fatalf("err = %v; want the failure already reported", err)
	}
}
