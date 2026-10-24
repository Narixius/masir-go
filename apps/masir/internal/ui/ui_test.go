package ui

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestWidth(t *testing.T) {
	for s, want := range map[string]int{
		"abc":               3,
		"🇮🇷 Iran":           7, // a flag is two regional indicators, 2 columns
		"سلام":              4,
		"\x1b[32mok\x1b[0m": 2,
		"日本":                4,
		"é":                1, // combining accent
	} {
		if got := Width(s); got != want {
			t.Errorf("Width(%q) = %d, want %d", s, got, want)
		}
	}
	if got := Truncate("\x1b[1mhello world\x1b[0m", 6); Width(got) != 6 {
		t.Errorf("Truncate width = %d (%q)", Width(got), got)
	}
}

func newTestPicker(multi bool) *Picker {
	return NewPicker(PickParams{
		Message: "pick", Multi: multi, AddCustom: true,
		Validate: func(v string) error {
			if v == "bad" {
				return os.ErrInvalid
			}
			return nil
		},
		Groups: []PickGroup{
			{Label: "Fruit", Items: []PickItem{{Value: "apple", Label: "Apple"}, {Value: "pear", Label: "Pear"}}},
			{Label: "Veg", Items: []PickItem{{Value: "kale", Label: "Kale", Search: "leafy"}}},
		},
	})
}

func keys(pk *Picker, ks ...key) (bool, []string, error) {
	for _, k := range ks {
		if done, vals, err := pk.handle(k); done {
			return done, vals, err
		}
	}
	return false, nil, nil
}

func typed(s string) []key {
	var out []key
	for _, r := range s {
		out = append(out, key{r: r})
	}
	return out
}

func TestPickerSingle(t *testing.T) {
	pk := newTestPicker(false)
	pk.cursor = pk.firstSelectable(pk.rows(), 0, 1)

	// Down then Enter picks the second item; Right jumps to the next group.
	if _, vals, _ := keys(pk, key{name: "down"}, key{name: "enter"}); !slices.Equal(vals, []string{"pear"}) {
		t.Fatalf("got %v, want pear", vals)
	}
	pk = newTestPicker(false)
	pk.cursor = pk.firstSelectable(pk.rows(), 0, 1)
	if _, vals, _ := keys(pk, key{name: "right"}, key{name: "enter"}); !slices.Equal(vals, []string{"kale"}) {
		t.Fatalf("group jump got %v, want kale", vals)
	}

	// The filter also matches Search text.
	pk = newTestPicker(false)
	if _, vals, _ := keys(pk, append(typed("leaf"), key{name: "enter"})...); !slices.Equal(vals, []string{"kale"}) {
		t.Fatalf("filter got %v", vals)
	}

	// Esc clears a filter first, then cancels.
	pk = newTestPicker(false)
	keys(pk, typed("zz")...)
	if done, _, _ := keys(pk, key{name: "esc"}); done || len(pk.filter) != 0 {
		t.Fatal("first Esc should only clear the filter")
	}
	if _, _, err := keys(pk, key{name: "esc"}); !IsCancel(err) {
		t.Fatalf("second Esc should cancel, got %v", err)
	}
}

func TestPickerMultiAndCustom(t *testing.T) {
	pk := newTestPicker(true)
	pk.cursor = pk.firstSelectable(pk.rows(), 0, 1)
	keys(pk, key{name: "space"}, key{name: "down"}, key{name: "space"}) // apple, pear
	keys(pk, typed("bad")...)
	keys(pk, key{name: "enter"}) // the add row rejects it
	if pk.errMsg == "" || slices.Contains(pk.order, "bad") {
		t.Fatalf("invalid typed value should be refused: err=%q order=%v", pk.errMsg, pk.order)
	}
	keys(pk, key{name: "clear"})
	keys(pk, typed("fig")...)
	keys(pk, key{name: "enter"}) // adds fig, clears the filter
	_, vals, _ := keys(pk, key{name: "enter"})
	if !slices.Equal(vals, []string{"apple", "pear", "fig"}) {
		t.Fatalf("got %v", vals)
	}
}

func TestParseKeys(t *testing.T) {
	got := parseKeys([]byte("\x1b[A\x1b[6~a\x7f\r\x1b"))
	want := []string{"up", "pgdn", "", "backspace", "enter", "esc"}
	if len(got) != len(want) {
		t.Fatalf("got %d keys: %+v", len(got), got)
	}
	for i, k := range got {
		if k.name != want[i] {
			t.Errorf("key %d = %q, want %q", i, k.name, want[i])
		}
	}
}

func TestPickerKeysAndSlashFilter(t *testing.T) {
	var ran []string
	pk := NewPicker(PickParams{
		Message: "pick",
		Groups:  []PickGroup{{Items: []PickItem{{Value: "a", Label: "Alpha"}, {Value: "l", Label: "Lima"}}}},
		Keys:    []PickKey{{Key: 'l', Help: "l retest", Run: func(v string) { ran = append(ran, v) }}},
	})
	pk.cursor = pk.firstSelectable(pk.rows(), 0, 1)

	keys(pk, key{r: 'l'}) // a shortcut, not a filter
	if len(pk.filter) != 0 || !slices.Equal(ran, []string{"a"}) {
		t.Fatalf("filter=%q ran=%v; want the shortcut on the cursor's item", string(pk.filter), ran)
	}
	keys(pk, key{r: '/'}, key{r: 'l'}, key{r: 'i'}) // now letters filter
	if string(pk.filter) != "li" || len(ran) != 1 {
		t.Fatalf("filter=%q ran=%v; after / letters should filter", string(pk.filter), ran)
	}
	if _, vals, _ := keys(pk, key{name: "enter"}); !slices.Equal(vals, []string{"l"}) {
		t.Fatalf("got %v, want the filtered match", vals)
	}

	pk = NewPicker(PickParams{Message: "pick", Groups: []PickGroup{{Items: []PickItem{{Value: "a", Label: "A"}}}},
		Keys: []PickKey{{Key: 'l', Run: func(string) {}}}})
	keys(pk, key{r: '/'})
	if done, _, _ := keys(pk, key{name: "esc"}); done || pk.typing {
		t.Fatal("Esc should leave filter mode before it cancels")
	}
}

func TestVisualKeepsColorCodesAndLines(t *testing.T) {
	t.Setenv("CLACK_RTL", "1")
	name := "سلام"
	plain := Visual(name)
	if plain == name {
		t.Fatal("RTL text should be reordered when bidi shaping is on")
	}
	colored := Visual("\x1b[36m" + name + "\x1b[39m done")
	if colored != "\x1b[36m"+plain+"\x1b[39m done" {
		t.Fatalf("color codes must pass through untouched: %q", colored)
	}
	if got := Visual(name + "\nok"); got != plain+"\nok" {
		t.Fatalf("each line is reordered on its own: %q", got)
	}
	if Visual("plain ascii") != "plain ascii" {
		t.Fatal("left-to-right text is unchanged")
	}
}

func TestBoldAllUndimsSoBoldLasts(t *testing.T) {
	got := boldAll("name  \x1b[2mvless\x1b[22m \x1b[32m(active)\x1b[39m  \x1b[33m770ms\x1b[39m")
	if strings.Contains(got, "\x1b[2m") || strings.Count(got, "\x1b[22m") > 1 {
		t.Errorf("dim codes inside would end the bold early: %q", got)
	}
	if !strings.Contains(got, "\x1b[32m(active)\x1b[39m") || !strings.Contains(got, "\x1b[33m770ms\x1b[39m") {
		t.Errorf("colors must survive: %q", got)
	}
}
