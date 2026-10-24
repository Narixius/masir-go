package main

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagSpec declares a flag: its name and optional one-letter alias ("proxy", "p").
// Bool flags take no value; many flags may be repeated and are comma-split.
type flagSpec struct {
	names []string
	bool  bool
	def   bool // a bool flag's default
	many  bool
	help  string
}

func addFlags(c *cobra.Command, specs []flagSpec) {
	for _, s := range specs {
		long, short := s.names[0], ""
		if len(s.names) > 1 {
			short = s.names[1]
		}
		switch {
		case s.bool:
			c.Flags().BoolP(long, short, s.def, s.help)
		case s.many:
			c.Flags().VarP(&listFlag{}, long, short, s.help)
		default:
			c.Flags().StringP(long, short, "", s.help)
		}
	}
}

// args are a command's parsed arguments, in the shape the commands read.
type args struct {
	pos   []string
	flags map[string][]string // only the flags that were given
}

// argsFrom collects what cobra parsed. A --no-x flag reads as x=false.
func argsFrom(cmd *cobra.Command, pos []string) args {
	a := args{pos: pos, flags: map[string][]string{}}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if base, ok := strings.CutPrefix(f.Name, "no-"); ok && f.Value.Type() == "bool" {
			if f.Value.String() == "true" {
				a.flags[base] = []string{"false"}
			}
			return
		}
		if l, ok := f.Value.(*listFlag); ok {
			a.flags[f.Name] = l.GetSlice()
			return
		}
		a.flags[f.Name] = []string{f.Value.String()}
	})
	return a
}

func (a args) has(name string) bool { _, ok := a.flags[name]; return ok }

func (a args) str(name string) string {
	v := a.flags[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// boolean returns the flag's value, or def when it wasn't given.
func (a args) boolean(name string, def bool) bool {
	v, ok := a.flags[name]
	if !ok {
		return def
	}
	return v[len(v)-1] != "false"
}

// list joins repeated flags and splits each on commas.
func (a args) list(name string) []string {
	var out []string
	for _, v := range a.flags[name] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func (a args) arg(i int) string {
	if i < len(a.pos) {
		return a.pos[i]
	}
	return ""
}

// listFlag is a repeatable string flag. It names its type "list" in help, where
// pflag's own StringArray would show "stringArray".
type listFlag []string

func (l *listFlag) String() string              { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error          { *l = append(*l, v); return nil }
func (l *listFlag) Type() string                { return "list" }
func (l *listFlag) GetSlice() []string          { return append([]string(nil), *l...) }
func (l *listFlag) Append(v string) error       { return l.Set(v) }
func (l *listFlag) Replace(vals []string) error { *l = append(listFlag(nil), vals...); return nil }
