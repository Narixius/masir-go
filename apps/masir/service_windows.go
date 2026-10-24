package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/narixius/masir-go/libs/masir-core/service"
	masird "github.com/narixius/masir-go/libs/masir-daemon"
)

// installService registers bin as an automatically started service serving user (a
// SID), or updates the existing one, then starts it. The service restarts after a
// failure, and after a non-zero exit, which is how an update restarts into the new
// binary. Needs Administrator rights.
func installService(bin, user string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	args := []string{"daemon", "run", "--user", user}
	s, err := m.OpenService(service.WindowsName)
	if err == nil {
		cfg, err := s.Config()
		if err != nil {
			s.Close()
			return err
		}
		cfg.BinaryPathName = commandLine(bin, args...)
		cfg.StartType = mgr.StartAutomatic
		if err := s.UpdateConfig(cfg); err != nil {
			s.Close()
			return err
		}
	} else {
		s, err = m.CreateService(service.WindowsName, bin, mgr.Config{
			DisplayName: "masir",
			Description: "masir daemon: Xray, routing rules and system-wide TUN",
			StartType:   mgr.StartAutomatic,
		}, args...)
		if err != nil {
			return err
		}
	}
	defer s.Close()
	restart := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
	}
	if err := s.SetRecoveryActions(restart, uint32((24 * time.Hour).Seconds())); err != nil {
		return err
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return err
	}
	stop(s)
	return s.Start()
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(service.WindowsName)
	if err != nil {
		return nil // not installed
	}
	defer s.Close()
	stop(s)
	return s.Delete()
}

func restartService() error {
	if !elevated() {
		return elevate("daemon", "restart")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(service.WindowsName)
	if err != nil {
		return err
	}
	defer s.Close()
	stop(s)
	return s.Start()
}

// stopServiceForInstall stops a running service first: Windows can't replace the
// executable of a running program.
func stopServiceForInstall() {
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()
	if s, err := m.OpenService(service.WindowsName); err == nil {
		stop(s)
		s.Close()
	}
}

// stop asks the service to stop and waits up to 30 seconds for it.
func stop(s *mgr.Service) {
	st, err := s.Query()
	if err != nil || st.State == svc.Stopped {
		return
	}
	if _, err := s.Control(svc.Stop); err != nil {
		return
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if st, err := s.Query(); err != nil || st.State == svc.Stopped {
			return
		}
	}
}

func commandLine(bin string, args ...string) string {
	line := windows.EscapeArg(bin)
	for _, a := range args {
		line += " " + windows.EscapeArg(a)
	}
	return line
}

// runDaemon runs the daemon; under the Service Control Manager it reports to it, so
// Windows can start, stop and restart it.
func runDaemon(opts masird.Options) error {
	if ok, err := svc.IsWindowsService(); err != nil || !ok {
		return masird.Run(opts)
	}
	if dir := os.Getenv("ProgramData"); dir != "" { // a service has no console for its log
		if err := os.MkdirAll(filepath.Join(dir, "masir"), 0o755); err == nil {
			if f, err := os.OpenFile(filepath.Join(dir, "masir", "masir.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
				log.SetOutput(f)
			}
		}
	}
	return svc.Run(service.WindowsName, &serviceHandler{opts: opts})
}

type serviceHandler struct{ opts masird.Options }

func (h *serviceHandler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := h.opts
	opts.Context, opts.NoReexec = ctx, true
	done := make(chan error, 1)
	go func() { done <- masird.Run(opts) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case err := <-done:
			switch {
			case errors.Is(err, masird.ErrRestart):
				log.Println("restarting into the updated binary")
				return false, 1 // a non-zero exit: the recovery actions start the new binary
			case err != nil:
				log.Println(err)
				return false, 2
			}
			return false, 0 // a client stopped the daemon: stay stopped
		}
	}
}
