package ui

import (
	"sync/atomic"

	"github.com/orochaa/go-clack/prompts"
)

// OnInterrupt runs when a spinner catches Ctrl-C. go-clack's spinner takes the signal
// for itself, so without this the work behind it would keep running.
var OnInterrupt = func() {}

var cancelShown atomic.Bool

// Cancelled tells the user they quit, once, even if a spinner already said so.
func Cancelled() {
	if cancelShown.Swap(true) || !styled {
		return
	}
	Outro(Gray("Cancelled"))
}

// Spinner is clack's spinner on a terminal. When output is piped it prints only the
// outcome, as a plain line.
type Spinner struct {
	s *prompts.SpinnerController
}

func Spin(msg string) *Spinner { return SpinWith(msg, "Cancelled") }

// SpinWith is Spin with what Ctrl-C prints, for work that goes on without the CLI.
func SpinWith(msg, cancelMsg string) *Spinner {
	if !styled {
		return &Spinner{}
	}
	s := prompts.Spinner(prompts.SpinnerOptions{
		CancelMessage: cancelMsg,
		OnCancel: func() {
			cancelShown.Store(true)
			OnInterrupt()
		},
	})
	s.Start(msg)
	return &Spinner{s: s}
}

func (s *Spinner) Message(msg string) {
	if s.s != nil && !s.s.IsCancelled {
		s.s.Message(msg)
	}
}

// Stop ends with the green success symbol.
func (s *Spinner) Stop(msg string) {
	switch {
	case s.s == nil:
		Step(msg)
	case !s.s.IsCancelled:
		s.s.Stop(msg, 0)
	}
}

// Fail ends with the red error symbol. Like Error, it puts msg in display order. A
// spinner the user already cancelled stays as is.
func (s *Spinner) Fail(msg string) {
	switch {
	case s.s == nil:
		Error(msg)
	case !s.s.IsCancelled:
		s.s.Stop(Red(Visual(msg)), 2)
	}
}
