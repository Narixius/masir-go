package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/orochaa/go-clack/prompts"
)

func main() {
	prompts.Intro("create-my-app")

	name, err := prompts.Text(prompts.TextParams{
		Message:     "Project name?",
		Placeholder: "my-app",
		Validate: func(value string) error {
			if len(value) == 0 {
				return errors.New("Name is required!")
			}
			return nil
		},
	})
	prompts.ExitOnError(err)

	framework, err := prompts.Select(prompts.SelectParams[string]{
		Message: "Pick a framework:",
		Options: []*prompts.SelectOption[string]{
			{Label: "React", Value: "react", Hint: "popular"},
			{Label: "Vue", Value: "vue"},
			{Label: "Svelte", Value: "svelte"},
		},
	})
	prompts.ExitOnError(err)

	tools, err := prompts.MultiSelect(prompts.MultiSelectParams[string]{
		Message: "Additional tools:",
		Options: []*prompts.MultiSelectOption[string]{
			{Label: "ESLint", Value: "eslint", Hint: "recommended"},
			{Label: "Prettier", Value: "prettier"},
			{Label: "GitHub Actions", Value: "gh-actions"},
		},
	})
	prompts.ExitOnError(err)

	lang, err := prompts.Select(prompts.SelectParams[string]{
		Message: "Pick a language:",
		Filter:  true,
		Options: []*prompts.SelectOption[string]{
			{Label: "آلمان", Value: "go"},
			{Label: "🇳🇱 هلند", Value: "ts"},
			{Label: "آلمان", Value: "rust"},
			{Label: "Python", Value: "py"},
		},
	})
	prompts.ExitOnError(err)

	install, err := prompts.Confirm(prompts.ConfirmParams{
		Message:      "Install dependencies?",
		InitialValue: true,
	})
	prompts.ExitOnError(err)

	if install {
		s := prompts.Spinner(prompts.SpinnerOptions{})
		s.Start("Installing dependencies")
		time.Sleep(2 * time.Second) // fake work
		s.Stop("Dependencies installed", 0)
	}

	prompts.Note(
		fmt.Sprintf("name: %s\nframework: %s\ntools: %v\nlang: %v", name, framework, tools, lang),
		prompts.NoteOptions{Title: "Summary"},
	)

	prompts.Outro("You're all !")
}
