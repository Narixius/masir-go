package main

// Installing what the app can give the system: the daemon as a background service, and
// masir as a command-line tool. Both run off the main thread and report by toast.

// cliState is the command-line tool as Settings shows it: read once, and again after an
// install or a removal.
func (a *app) cliState() cliStatus {
	if !a.cliChecked {
		a.cliChecked = true
		a.cli = cliStatusNow(a.cliBinary())
	}
	return a.cli
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
			if st.onPath {
				a.notify("masir is installed and on your PATH")
			} else {
				a.notify("masir is installed in " + st.dir + "; add it to your PATH to use it")
			}
		})
	})
}

func (a *app) removeCommandLine() {
	if a.cliBusy {
		return
	}
	bin := a.cliBinary()
	a.cliBusy = true
	a.async(func() {
		err := removeCLI(bin)
		a.post(func() {
			a.cliBusy = false
			if err != nil {
				a.fail("Couldn't remove the command line tool: ", err)
				return
			}
			a.cliChecked = false
			a.notify("The masir command was removed")
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
			a.notify("The background service is installed")
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
			a.notify("The background service is removed")
		})
	})
}
