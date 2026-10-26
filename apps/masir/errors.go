package main

import (
	"errors"
	"strings"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
)

// How masir reports a failure, everywhere:
//
//	✖  What happened, in one line.
//	│
//	│  Details, one per line, if there are any.
//	│
//	│  What to do, ending in a colon when commands follow:
//	   masir some command
//	   masir another command
//	│
//	│  A last line, if it helps: what was left untouched.
//
// Commands sit on lines of their own, with no rail and no color, so that selecting one
// copies exactly the command. They are never buried in a sentence.

// advice is what to do about a problem: a sentence, then commands to run.
type advice struct {
	text string
	cmds []string
}

func (a advice) empty() bool { return a.text == "" && len(a.cmds) == 0 }

// plain is the advice as ordinary lines, for places that can only show a string.
func (a advice) plain() string {
	lines := []string{}
	if a.text != "" {
		lines = append(lines, a.text)
	}
	for _, c := range a.cmds {
		lines = append(lines, "  "+c)
	}
	return strings.Join(lines, "\n")
}

// userError is a failure that already knows how to be shown.
type userError struct {
	headline string
	details  string // lines under the headline; may be empty
	advice   advice
	note     string // a last line, e.g. what was left as it was
}

func (e *userError) Error() string {
	parts := []string{e.headline}
	if e.details != "" {
		parts = append(parts, e.details)
	}
	if !e.advice.empty() {
		parts = append(parts, e.advice.plain())
	}
	if e.note != "" {
		parts = append(parts, e.note)
	}
	return strings.Join(parts, "\n")
}

// sentence makes a daemon's message read as a sentence: capitalized, ending in a period.
func sentence(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return msg
	}
	return capitalize(strings.TrimSuffix(msg, ".")) + "."
}

// advised is a failure with a next step: text says what to do, cmds are what to run.
func advised(headline, text string, cmds ...string) error {
	return &userError{headline: headline, advice: advice{text: text, cmds: cmds}}
}

// printFailure shows a failure in the format above.
func printFailure(headline, details string, adv advice, note string) {
	ui.Error(headline)
	printFailureBody(details, adv, note)
}

// printFailureBody prints what follows the headline, for callers that showed the headline
// themselves (a spinner or progress bar that failed).
func printFailureBody(details string, adv advice, note string) {
	if details != "" {
		ui.Gap()
		ui.Message(ui.Visual(details))
	}
	if !adv.empty() {
		ui.Gap()
		ui.Advise(adv.text, adv.cmds)
	}
	if note != "" {
		ui.Gap()
		ui.Message(ui.Dim(note))
	}
}

// asUserError returns err's userError, if it is or wraps one.
func asUserError(err error) (*userError, bool) {
	var ue *userError
	if errors.As(err, &ue) {
		return ue, true
	}
	return nil, false
}

// failer is a spinner or progress bar that can end in a failure line.
type failer interface{ Fail(msg string) }

// failWith ends a spinner or progress bar with err's headline (after prefix), then shows
// the rest of the failure under it in the common format.
func failWith(f failer, prefix string, err error) {
	headline, adv := describe(err)
	details, note := "", ""
	if ue, ok := asUserError(err); ok {
		details, note = ue.details, ue.note
	}
	f.Fail(prefix + headline)
	printFailureBody(details, adv, note)
}
