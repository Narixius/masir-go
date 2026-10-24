package ui

import (
	"errors"
	"os"

	"github.com/orochaa/go-clack/core"
	"github.com/orochaa/go-clack/prompts"
	"golang.org/x/term"
)

// ErrCancel is returned when the user cancels a prompt (Esc or Ctrl-C).
var ErrCancel = core.ErrCancelPrompt

// ErrNoTTY is returned by prompts that need an answer but have no terminal to ask on.
var ErrNoTTY = errors.New("this needs an interactive terminal; pass the value as an argument or flag")

func IsCancel(err error) bool { return errors.Is(err, ErrCancel) }

// Interactive reports whether there's a terminal to prompt on.
func Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

type Option[T comparable] struct {
	Label string
	Hint  string
	Value T
}

// Text asks for a string. def is shown as a placeholder and returned on empty input;
// without a terminal def is returned, or ErrNoTTY when there's none.
func Text(msg, def string, validate func(string) error) (string, error) {
	if !Interactive() {
		if def == "" {
			return "", ErrNoTTY
		}
		return def, nil
	}
	for {
		v, err := prompts.Text(prompts.TextParams{Message: msg, Placeholder: def})
		if err != nil {
			return "", err
		}
		if v == "" {
			v = def
		}
		if validate != nil {
			if err := validate(v); err != nil {
				Error(Red(err.Error()))
				continue
			}
		}
		return v, nil
	}
}

func Confirm(msg string, def bool) (bool, error) {
	if !Interactive() {
		return def, nil
	}
	return prompts.Confirm(prompts.ConfirmParams{Message: msg, InitialValue: def})
}

// Select picks one option; long lists get type-to-filter. Without a terminal it
// returns initial, or the first option.
func Select[T comparable](msg string, opts []Option[T], initial T) (T, error) {
	if !Interactive() {
		var zero T
		if initial != zero || len(opts) == 0 {
			return initial, nil
		}
		return opts[0].Value, nil
	}
	// go-clack gets positions, not values: it swaps an empty string value for the
	// option's label, and would confuse options that share a label.
	options := make([]*prompts.SelectOption[int], len(opts))
	start := 0
	for i, o := range opts {
		options[i] = &prompts.SelectOption[int]{Label: o.Label, Hint: o.Hint, Value: i}
		if o.Value == initial {
			start = i
		}
	}
	i, err := prompts.Select(prompts.SelectParams[int]{Message: msg, Options: options, InitialValue: start, Filter: len(opts) > 12})
	if err != nil {
		var zero T
		return zero, err
	}
	return opts[i].Value, nil
}

// MultiSelect picks any number of options; required refuses an empty answer.
func MultiSelect[T comparable](msg string, opts []Option[T], initial []T, required bool) ([]T, error) {
	if !Interactive() {
		if required && len(initial) == 0 {
			return nil, ErrNoTTY
		}
		return initial, nil
	}
	selected := map[T]bool{}
	for _, v := range initial {
		selected[v] = true
	}
	options := make([]*prompts.MultiSelectOption[int], len(opts))
	for i, o := range opts {
		options[i] = &prompts.MultiSelectOption[int]{Label: o.Label, Hint: o.Hint, Value: i, IsSelected: selected[o.Value]}
	}
	picked, err := prompts.MultiSelect(prompts.MultiSelectParams[int]{Message: msg, Options: options, Required: required, Filter: len(opts) > 12})
	if err != nil {
		return nil, err
	}
	out := make([]T, len(picked))
	for j, i := range picked {
		out[j] = opts[i].Value
	}
	return out, nil
}
