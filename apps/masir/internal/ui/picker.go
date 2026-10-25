package ui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/orochaa/go-clack/prompts/symbols"
	"golang.org/x/term"
)

type PickItem struct {
	Value  string
	Label  string
	Hint   string // static text after the label, styled by the caller
	Search string // more text the filter matches, beside the label
	Suffix string // shown after the label; SetSuffix can change it live
}

// PickSource is a long list searched as the user types. Load runs once, in the
// background, on the first keystroke; matches show in their own group.
type PickSource struct {
	Label string
	Load  func() ([]PickItem, error)
	Limit int // most matches shown; 0 means 30
}

type PickKey struct {
	Key  rune
	Help string             // shown in the help line, e.g. "l retest"
	Run  func(value string) // the item under the cursor, or "" when there is none
}

type PickGroup struct {
	Label string // empty: no header
	Hint  string
	Items []PickItem
}

type PickParams struct {
	Message  string
	Groups   []PickGroup
	Initial  []string // preselected (multi), or where the cursor starts (single)
	Multi    bool
	Required bool // multi: refuse an empty selection

	// AddCustom lets the user type values that aren't listed; Validate checks them.
	AddCustom   bool
	CustomLabel string
	Validate    func(string) error

	// OnVisible gets the values on screen whenever they change. It runs on the picker's
	// loop, so it must hand slow work (like latency probes) to a goroutine.
	OnVisible func(values []string)

	// More offers items beyond Groups once the user types, loaded lazily: the geo files'
	// full category lists, say.
	More *PickSource

	// Keys are single-letter shortcuts acting on the item under the cursor. A picker
	// with keys filters only after "/", so typing a letter never collides with them.
	Keys []PickKey
}

// Picker is a filterable, grouped list whose rows can update live (latency results).
type Picker struct {
	p        PickParams
	selected map[string]bool
	order    []string // selection order, for the summary
	custom   []PickItem
	filter   []rune
	more     moreState
	typing   bool // in a picker with Keys: "/" was pressed, letters now filter
	cursor   int
	offset   int
	errMsg   string
	frame    int
	lines    int

	mu      sync.Mutex
	suffix  map[string]string
	pending map[string]bool
	dirty   bool

	lastVisible string
}

func NewPicker(p PickParams) *Picker {
	pk := &Picker{p: p, selected: map[string]bool{}, suffix: map[string]string{}, pending: map[string]bool{}}
	if p.Multi {
		listed := map[string]bool{}
		for _, g := range p.Groups {
			for _, it := range g.Items {
				listed[it.Value] = true
			}
		}
		for _, v := range p.Initial {
			pk.selected[v] = true
			pk.order = append(pk.order, v)
			if !listed[v] { // a value typed earlier: keep it visible so it can be unselected
				pk.custom = append(pk.custom, PickItem{Value: v, Label: v})
			}
		}
	}
	if p.CustomLabel == "" {
		pk.p.CustomLabel = "Your values"
	}
	return pk
}

// SetSuffix updates a row's suffix; pending shows a spinner instead. Safe from any goroutine.
func (pk *Picker) SetSuffix(value, suffix string, pending bool) {
	pk.mu.Lock()
	pk.suffix[value] = suffix
	pk.pending[value] = pending
	pk.dirty = true
	pk.mu.Unlock()
}

// PickOne runs a single-choice picker.
func PickOne(p PickParams) (string, error) {
	vals, err := NewPicker(p).Run()
	if err != nil || len(vals) == 0 {
		return "", err
	}
	return vals[0], nil
}

type rowKind int

const (
	rowHeader rowKind = iota
	rowItem
	rowAdd
)

type row struct {
	kind rowKind
	item PickItem
	hint string
}

// moreState is a PickSource's loading progress. Guarded by Picker.mu.
type moreState struct {
	started bool
	loaded  bool
	items   []PickItem
	err     string
}

// words splits a query into lowercase words; hyphens, underscores and colons count as
// spaces, so "ai chat" finds category-ai-chat.
func words(s string) []string {
	return strings.Fields(strings.NewReplacer("-", " ", "_", " ", ":", " ").Replace(strings.ToLower(s)))
}

// matchWords reports whether every query word appears in the item's text, or the query
// typed without spaces appears in the text without them ("aichat" finds ai-chat).
func matchWords(q []string, it PickItem) bool {
	all := words(it.Label + " " + it.Search + " " + it.Value)
	text := strings.Join(all, " ")
	ok := true
	for _, w := range q {
		if !strings.Contains(text, w) {
			ok = false
			break
		}
	}
	return ok || strings.Contains(strings.Join(all, ""), strings.Join(q, ""))
}

// loadMore starts loading the PickSource, once.
func (pk *Picker) loadMore() {
	pk.mu.Lock()
	defer pk.mu.Unlock()
	if pk.p.More == nil || pk.more.started {
		return
	}
	pk.more.started = true
	go func() {
		items, err := pk.p.More.Load()
		pk.mu.Lock()
		pk.more.loaded, pk.more.items = true, items
		if err != nil {
			pk.more.err = err.Error()
		}
		pk.dirty = true
		pk.mu.Unlock()
	}()
}

// moreRows are the PickSource's matches for q, best first, skipping items listed in
// the groups already.
func (pk *Picker) moreRows(q []string, listed map[string]bool) []row {
	if pk.p.More == nil || len(q) == 0 {
		return nil
	}
	pk.mu.Lock()
	st := pk.more
	pk.mu.Unlock()
	header := row{kind: rowHeader, item: PickItem{Label: pk.p.More.Label}}
	switch {
	case !st.loaded:
		header.hint = "loading…"
		return []row{header}
	case st.err != "":
		header.hint = st.err
		return []row{header}
	}
	var found []PickItem
	for _, it := range st.items {
		if !listed[it.Value] && matchWords(q, it) {
			found = append(found, it)
		}
	}
	if len(found) == 0 {
		return nil
	}
	whole := strings.Join(q, " ")
	score := func(it PickItem) int { // lower is better: exact, then prefix, then shorter
		label, value := strings.Join(words(it.Label), " "), strings.Join(words(it.Value), " ")
		switch {
		case label == whole || strings.HasSuffix(value, " "+whole):
			return 0
		case strings.HasPrefix(label, whole):
			return 1
		case strings.Contains(label, whole):
			return 2
		}
		return 3
	}
	slices.SortStableFunc(found, func(a, b PickItem) int {
		if d := score(a) - score(b); d != 0 {
			return d
		}
		return len(a.Label) - len(b.Label)
	})
	limit := pk.p.More.Limit
	if limit <= 0 {
		limit = 30
	}
	header.hint = fmt.Sprintf("%d match%s", len(found), plural(len(found), "es"))
	if len(found) > limit {
		header.hint = fmt.Sprintf("best %d of %d; type more to narrow", limit, len(found))
		found = found[:limit]
	}
	rows := []row{header}
	for _, it := range found {
		rows = append(rows, row{kind: rowItem, item: it})
	}
	return rows
}

func (pk *Picker) rows() []row {
	q := words(string(pk.filter))
	match := func(it PickItem) bool { return len(q) == 0 || matchWords(q, it) }
	listed := map[string]bool{}
	var rows []row
	exact := false
	groups := pk.p.Groups
	if len(pk.custom) > 0 {
		groups = append([]PickGroup{{Label: pk.p.CustomLabel, Items: pk.custom}}, groups...)
	}
	for _, g := range groups {
		var items []row
		for _, it := range g.Items {
			listed[it.Value] = true
			if strings.EqualFold(it.Value, string(pk.filter)) {
				exact = true
			}
			if match(it) {
				items = append(items, row{kind: rowItem, item: it})
			}
		}
		if len(items) == 0 {
			continue
		}
		if g.Label != "" {
			rows = append(rows, row{kind: rowHeader, item: PickItem{Label: g.Label}, hint: g.Hint})
		}
		rows = append(rows, items...)
	}
	more := pk.moreRows(q, listed)
	for _, r := range more {
		if r.kind == rowItem && strings.EqualFold(r.item.Value, string(pk.filter)) {
			exact = true
		}
	}
	rows = append(rows, more...)
	// After the matches, so the cursor lands on a listed item when one matches.
	if pk.p.AddCustom && len(pk.filter) > 0 && !exact {
		v := strings.TrimSpace(string(pk.filter))
		rows = append(rows, row{kind: rowAdd, item: PickItem{Value: v, Label: v}})
	}
	return rows
}

func selectable(r row) bool { return r.kind != rowHeader }

// Run shows the picker and returns the chosen values. Without a terminal it returns
// Initial, or ErrNoTTY when an answer is required.
func (pk *Picker) Run() ([]string, error) {
	if !Interactive() {
		initial := slices.DeleteFunc(slices.Clone(pk.p.Initial), func(v string) bool { return v == "" })
		if len(initial) == 0 && (pk.p.Required || !pk.p.Multi) {
			return nil, ErrNoTTY
		}
		return initial, nil
	}
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer term.Restore(fd, old)
	os.Stdout.WriteString("\x1b[?25l")
	defer os.Stdout.WriteString("\x1b[?25h")

	rows := pk.rows()
	pk.cursor = pk.firstSelectable(rows, 0, 1)
	if !pk.p.Multi && len(pk.p.Initial) > 0 {
		for i, r := range rows {
			if r.kind == rowItem && r.item.Value == pk.p.Initial[0] {
				pk.cursor = i
			}
		}
	}
	pk.render(false, "")

	buf := make([]byte, 256)
	tick := time.Now()
	for {
		if readable(fd, 80*time.Millisecond) {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return nil, err
			}
			for _, k := range parseKeys(buf[:n]) {
				done, vals, err := pk.handle(k)
				if done {
					return vals, err
				}
			}
			pk.render(false, "")
			continue
		}
		pk.mu.Lock()
		changed := pk.dirty
		pk.dirty = false
		anyPending := false
		for _, p := range pk.pending {
			anyPending = anyPending || p
		}
		pk.mu.Unlock()
		if anyPending && time.Since(tick) >= 80*time.Millisecond {
			pk.frame++
			tick = time.Now()
			changed = true
		}
		if changed {
			pk.render(false, "")
		}
	}
}

type key struct {
	name string // "up", "down", "left", "right", "home", "end", "pgup", "pgdn", "tab", "backtab", "enter", "space", "backspace", "esc", "cancel", "clear", or "" for a typed rune
	r    rune
}

func parseKeys(b []byte) []key {
	var keys []key
	for len(b) > 0 {
		if b[0] == 0x1b {
			if len(b) == 1 {
				return append(keys, key{name: "esc"})
			}
			seq, n := escapeSeq(b)
			if seq != "" {
				keys = append(keys, key{name: seq})
			}
			b = b[n:]
			continue
		}
		switch b[0] {
		case 3:
			keys = append(keys, key{name: "cancel"})
		case '\r', '\n':
			keys = append(keys, key{name: "enter"})
		case '\t':
			keys = append(keys, key{name: "tab"})
		case 0x7f, 0x08:
			keys = append(keys, key{name: "backspace"})
		case 0x15:
			keys = append(keys, key{name: "clear"})
		case ' ':
			keys = append(keys, key{name: "space", r: ' '})
		default:
			r, n := utf8.DecodeRune(b)
			if unicode.IsPrint(r) {
				keys = append(keys, key{r: r})
			}
			b = b[n:]
			continue
		}
		b = b[1:]
	}
	return keys
}

func escapeSeq(b []byte) (string, int) {
	if len(b) < 3 || (b[1] != '[' && b[1] != 'O') {
		return "esc", 1
	}
	switch b[2] {
	case 'A':
		return "up", 3
	case 'B':
		return "down", 3
	case 'C':
		return "right", 3
	case 'D':
		return "left", 3
	case 'H':
		return "home", 3
	case 'F':
		return "end", 3
	case 'Z':
		return "backtab", 3
	}
	if len(b) >= 4 && b[3] == '~' {
		switch b[2] {
		case '1', '7':
			return "home", 4
		case '4', '8':
			return "end", 4
		case '5':
			return "pgup", 4
		case '6':
			return "pgdn", 4
		}
	}
	n := 2 // skip an unknown CSI sequence whole
	for n < len(b) && (b[n] < 0x40 || b[n] > 0x7e) {
		n++
	}
	return "", min(n+1, len(b))
}

func (pk *Picker) handle(k key) (done bool, vals []string, err error) {
	rows := pk.rows()
	pk.errMsg = ""
	cur := func() (row, bool) {
		if pk.cursor >= 0 && pk.cursor < len(rows) {
			return rows[pk.cursor], true
		}
		return row{}, false
	}
	switch k.name {
	case "cancel":
		pk.render(true, "")
		return true, nil, ErrCancel
	case "esc":
		if len(pk.filter) > 0 || pk.typing {
			pk.typing = false
			pk.setFilter(nil)
			return
		}
		pk.render(true, "")
		return true, nil, ErrCancel
	case "up":
		pk.cursor = pk.firstSelectable(rows, pk.cursor-1, -1)
	case "down":
		pk.cursor = pk.firstSelectable(rows, pk.cursor+1, 1)
	case "pgup":
		pk.cursor = pk.firstSelectable(rows, max(pk.cursor-pk.window(), 0), 1)
	case "pgdn":
		pk.cursor = pk.firstSelectable(rows, min(pk.cursor+pk.window(), len(rows)-1), -1)
	case "home":
		pk.cursor = pk.firstSelectable(rows, 0, 1)
	case "end":
		pk.cursor = pk.firstSelectable(rows, len(rows)-1, -1)
	case "left", "backtab":
		pk.cursor = pk.jumpGroup(rows, -1)
	case "right", "tab":
		pk.cursor = pk.jumpGroup(rows, 1)
	case "backspace":
		if len(pk.filter) > 0 {
			pk.setFilter(pk.filter[:len(pk.filter)-1])
		} else {
			pk.typing = false
		}
	case "clear":
		pk.setFilter(nil)
	case "space":
		// In a multi-select, space always selects, as in every list; a search is typed
		// without spaces ("aichat" still finds ai-chat, see matchWords).
		if !pk.p.Multi {
			if pk.filtering() {
				pk.setFilter(append(pk.filter, ' '))
			}
			return
		}
		if r, ok := cur(); ok {
			if r.kind == rowAdd {
				pk.addCustom(r.item.Value)
			} else {
				pk.toggle(r.item.Value)
			}
		}
	case "enter":
		r, ok := cur()
		if pk.p.Multi {
			if ok && r.kind == rowAdd {
				pk.addCustom(r.item.Value)
				return
			}
			if pk.p.Required && len(pk.order) == 0 {
				pk.errMsg = "select at least one (space)"
				return
			}
			pk.render(true, pk.summary())
			return true, slices.Clone(pk.order), nil
		}
		if !ok {
			return
		}
		if r.kind == rowAdd && pk.p.Validate != nil {
			if verr := pk.p.Validate(r.item.Value); verr != nil {
				pk.errMsg = fmt.Sprintf("%q: %v", r.item.Value, verr)
				return
			}
		}
		pk.render(true, Visual(r.item.Label))
		return true, []string{r.item.Value}, nil
	default:
		switch {
		case k.r == 0:
		case pk.filtering():
			pk.setFilter(append(pk.filter, k.r))
		case k.r == '/':
			pk.typing = true
		default:
			for _, sk := range pk.p.Keys {
				if sk.Key == k.r {
					v := ""
					if r, ok := cur(); ok && r.kind == rowItem {
						v = r.item.Value
					}
					sk.Run(v)
				}
			}
		}
	}
	return
}

// filtering reports whether typed letters go to the filter: always without Keys,
// and after "/" with them.
func (pk *Picker) filtering() bool { return len(pk.p.Keys) == 0 || pk.typing }

func (pk *Picker) setFilter(f []rune) {
	pk.filter = f
	if len(f) > 0 {
		pk.loadMore()
	}
	pk.offset = 0
	pk.cursor = pk.firstSelectable(pk.rows(), 0, 1)
}

func (pk *Picker) toggle(v string) {
	if pk.selected[v] {
		delete(pk.selected, v)
		pk.order = slices.DeleteFunc(pk.order, func(s string) bool { return s == v })
		return
	}
	pk.selected[v] = true
	pk.order = append(pk.order, v)
	// A pick found by search joins the custom items, so it stays listed (and
	// deselectable) once the search is cleared.
	if it, ok := pk.moreItem(v); ok && !pk.listed(v) {
		pk.custom = append(pk.custom, it)
	}
}

// listed reports whether v is among the groups' or custom items already.
func (pk *Picker) listed(v string) bool {
	has := func(it PickItem) bool { return it.Value == v }
	if slices.ContainsFunc(pk.custom, has) {
		return true
	}
	for _, g := range pk.p.Groups {
		if slices.ContainsFunc(g.Items, has) {
			return true
		}
	}
	return false
}

func (pk *Picker) moreItem(v string) (PickItem, bool) {
	pk.mu.Lock()
	defer pk.mu.Unlock()
	for _, it := range pk.more.items {
		if it.Value == v {
			return it, true
		}
	}
	return PickItem{}, false
}

func (pk *Picker) addCustom(v string) {
	if pk.p.Validate != nil {
		if err := pk.p.Validate(v); err != nil {
			pk.errMsg = fmt.Sprintf("%q: %v", v, err)
			return
		}
	}
	if !slices.ContainsFunc(pk.custom, func(it PickItem) bool { return it.Value == v }) {
		pk.custom = append(pk.custom, PickItem{Value: v, Label: v})
	}
	if !pk.selected[v] {
		pk.toggle(v)
	}
	pk.setFilter(nil)
}

func (pk *Picker) firstSelectable(rows []row, from, dir int) int {
	n := len(rows)
	if n == 0 {
		return 0
	}
	for i := range n {
		j := ((from+i*dir)%n + n) % n // wraps both ways
		if selectable(rows[j]) {
			return j
		}
	}
	return 0
}

func (pk *Picker) jumpGroup(rows []row, dir int) int {
	i := pk.cursor + dir
	for i >= 0 && i < len(rows) && rows[i].kind != rowHeader {
		i += dir
	}
	if dir < 0 { // to the previous group's header
		i--
		for i >= 0 && rows[i].kind != rowHeader {
			i--
		}
	}
	if i < 0 || i >= len(rows) {
		return pk.cursor
	}
	return pk.firstSelectable(rows, i+1, 1)
}

func (pk *Picker) window() int {
	return min(max(termHeight()-7, 5), 20)
}

func (pk *Picker) labelFor(v string) string {
	if it, ok := pk.moreItem(v); ok {
		return it.Label
	}
	for _, it := range pk.custom {
		if it.Value == v {
			return it.Label
		}
	}
	for _, g := range pk.p.Groups {
		for _, it := range g.Items {
			if it.Value == v {
				return it.Label
			}
		}
	}
	return v
}

func (pk *Picker) summary() string {
	if len(pk.order) == 0 {
		return "nothing selected"
	}
	var labels []string
	for _, v := range pk.order[:min(3, len(pk.order))] {
		labels = append(labels, Visual(pk.labelFor(v)))
	}
	s := strings.Join(labels, ", ")
	if len(pk.order) > 3 {
		s += fmt.Sprintf(" +%d more", len(pk.order)-3)
	}
	return s
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// render redraws the picker in place. final collapses it to clack's answered form:
// summary set on submit, empty on cancel.
func (pk *Picker) render(final bool, summary string) {
	width := TermWidth()
	var lines []string
	add := func(s string) { lines = append(lines, Truncate(s, width-1)) }

	if final {
		add(Gray(symbols.BAR))
		if summary == "" {
			add(Red(symbols.STEP_CANCEL) + "  " + pk.p.Message)
			add(Gray(symbols.BAR) + "  " + Dim("cancelled"))
		} else {
			add(Green(symbols.STEP_SUBMIT) + "  " + pk.p.Message)
			add(Gray(symbols.BAR) + "  " + Dim(summary))
		}
		pk.draw(lines, true)
		return
	}

	rows := pk.rows()
	bar := Cyan(symbols.BAR) + "  "
	head := Cyan(symbols.STEP_ACTIVE) + "  " + Bold(pk.p.Message)
	if pk.p.Multi {
		head += Dim(fmt.Sprintf(" · %d selected", len(pk.order)))
	}
	add(Gray(symbols.BAR))
	add(head)
	if len(pk.filter) > 0 || pk.typing {
		matches := 0
		for _, r := range rows {
			if r.kind == rowItem {
				matches++
			}
		}
		add(bar + Dim("filter: ") + Visual(string(pk.filter)) + "▏ " + Dim(fmt.Sprintf("%d match%s", matches, plural(matches, "es"))))
	}

	win := pk.window()
	if pk.cursor < pk.offset {
		pk.offset = pk.cursor
	}
	if pk.cursor >= pk.offset+win {
		pk.offset = pk.cursor - win + 1
	}
	// Show a group's header above its first visible item.
	if pk.offset > 0 && pk.offset < len(rows) && rows[pk.offset-1].kind == rowHeader && pk.cursor == pk.offset {
		pk.offset--
	}
	end := min(pk.offset+win, len(rows))

	pk.mu.Lock()
	var visible []string
	if pk.offset > 0 {
		add(bar + Dim(fmt.Sprintf("↑ %d more", pk.offset)))
	}
	for i := pk.offset; i < end; i++ {
		r := rows[i]
		switch r.kind {
		case rowHeader:
			h := Cyan("▸ " + Visual(r.item.Label))
			if r.hint != "" {
				h += "  " + Dim(r.hint)
			}
			add(bar + h)
		case rowAdd:
			label := "+ add “" + Visual(r.item.Label) + "”"
			if i == pk.cursor {
				add(bar + Green("→ ") + Bold(label))
			} else {
				add(bar + "  " + label)
			}
		case rowItem:
			visible = append(visible, r.item.Value)
			add(bar + pk.itemLine(r.item, i == pk.cursor))
		}
	}
	pk.mu.Unlock()
	if end < len(rows) {
		add(bar + Dim(fmt.Sprintf("↓ %d more", len(rows)-end)))
	}
	if len(rows) == 0 {
		add(bar + Dim("no matches"))
	}
	if pk.errMsg != "" {
		add(bar + Red(pk.errMsg))
	}
	add(bar + Gray(pk.help(rows)))
	add(Cyan(symbols.BAR_END))
	pk.draw(lines, false)

	if pk.p.OnVisible != nil {
		if key := strings.Join(visible, "\x00"); key != pk.lastVisible {
			pk.lastVisible = key
			pk.p.OnVisible(visible)
		}
	}
}

// itemLine expects pk.mu held, for the live suffix.
// boldAll makes s bold, colors kept. Dim text is undimmed first: bold and dim share a
// reset code, so dim inside bold would end the bold early.
func boldAll(s string) string {
	s = strings.ReplaceAll(s, "\x1b[2m", "")
	s = strings.ReplaceAll(s, "\x1b[22m", "")
	return Bold(s)
}

func (pk *Picker) itemLine(it PickItem, atCursor bool) string {
	label := Visual(it.Label)
	var mark string
	switch {
	case pk.p.Multi && pk.selected[it.Value]:
		mark = Green(symbols.CHECKBOX_SELECTED)
	case pk.p.Multi:
		mark = Gray(symbols.CHECKBOX_INACTIVE)
	case atCursor:
		mark = Green(symbols.RADIO_ACTIVE)
	default:
		mark = Gray(symbols.RADIO_INACTIVE)
	}
	rest := label
	if it.Hint != "" {
		rest += "  " + it.Hint
	}
	suffix := it.Suffix
	if s, ok := pk.suffix[it.Value]; ok {
		suffix = s
	}
	if pk.pending[it.Value] {
		suffix = Gray(spinFrames[pk.frame%len(spinFrames)])
	}
	if suffix != "" {
		rest += "  " + suffix
	}
	if atCursor { // the whole row stands out: name, hint and latency
		return Green("→ ") + mark + " " + boldAll(rest)
	}
	return "  " + mark + " " + rest
}

func (pk *Picker) help(rows []row) string {
	parts := []string{"↑/↓ navigate"}
	if slices.ContainsFunc(rows, func(r row) bool { return r.kind == rowHeader }) {
		parts = append(parts, "←/→ group")
	}
	if pk.p.Multi {
		parts = append(parts, "space select")
	}
	switch {
	case len(pk.p.Keys) > 0:
		for _, k := range pk.p.Keys {
			parts = append(parts, k.Help)
		}
		parts = append(parts, "/ filter")
	case pk.p.AddCustom:
		parts = append(parts, "type to filter or add")
	default:
		parts = append(parts, "type to filter")
	}
	parts = append(parts, "enter confirm")
	return strings.Join(parts, " • ")
}

func (pk *Picker) draw(lines []string, final bool) {
	var b strings.Builder
	if pk.lines > 0 {
		b.WriteString("\r")
		if pk.lines > 1 {
			fmt.Fprintf(&b, "\x1b[%dA", pk.lines-1)
		}
		b.WriteString("\x1b[J")
	}
	b.WriteString(strings.Join(lines, "\r\n"))
	if final {
		b.WriteString("\r\n")
	}
	os.Stdout.WriteString(b.String())
	pk.lines = len(lines)
}

func plural(n int, suffix string) string {
	if n == 1 {
		return ""
	}
	return suffix
}
