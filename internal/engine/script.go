//go:build darwin

package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// Quote makes a string safe to paste into a shell.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=@%+,~", r):
		default:
			safe = false
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func quoteAll(args []string, home string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if home != "" {
			a = humanize.Tilde(a, home)
		}
		parts[i] = Quote(a)
	}
	return strings.Join(parts, " ")
}

// Argv is the command a step stands for. For a removal it is the shell
// command that does the same thing.
func Argv(s recipe.Step) []string {
	if s.Remove == nil {
		if s.Sudo {
			return append([]string{"sudo"}, s.Cmd...)
		}
		return s.Cmd
	}
	if s.Remove.ToTrash {
		return append([]string{"trash"}, s.Remove.Paths...)
	}
	return append([]string{"rm", "-rf", "--"}, s.Remove.Paths...)
}

// Display is the one-line command shown on the plan screen. With a home
// directory, paths under it are shortened to ~/…; long path lists are cut.
func Display(s recipe.Step, home string) string {
	argv := Argv(s)
	const keep = 3
	if s.Remove != nil && len(s.Remove.Paths) > keep {
		head := len(argv) - len(s.Remove.Paths) + keep
		return fmt.Sprintf("%s … +%s more", quoteAll(argv[:head], home),
			humanize.Count(int64(len(s.Remove.Paths)-keep)))
	}
	return quoteAll(argv, home)
}

// Script renders a plan as a shell script that does the same thing.
func Script(p recipe.Plan, now time.Time) string {
	var b strings.Builder
	groups := len(Groups(p))
	what := humanize.Plural(len(p.Steps), "step")
	if groups > 0 {
		what += " in " + humanize.Plural(groups, p.Noun())
	}
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# space plan: %s — %s\n", oneLine(p.Title), what)
	fmt.Fprintf(&b, "# Generated on %s. Read it before you run it.\n", now.Format("2006-01-02"))
	if groups > 0 {
		scriptGroupsNote(&b, p)
	}
	b.WriteString("set -eu\n")
	if groups > 0 {
		b.WriteString("failed=\n")
	}
	for _, st := range Layout(p) {
		if len(st.Groups) == 0 {
			b.WriteString("\n")
			scriptStep(&b, p, st.Step, "", "")
			continue
		}
		for _, g := range st.Groups {
			scriptGroup(&b, p, g)
		}
	}
	if groups > 0 {
		scriptGroupsEnd(&b)
	}
	return b.String()
}

// scriptStep writes one step: what it is, why, and its command. indent goes
// in front of every line, and tail after the command (" &&" inside a group).
func scriptStep(b *strings.Builder, p recipe.Plan, i int, indent, tail string) {
	s := p.Steps[i]
	fmt.Fprintf(b, "%s# %d. %s — %s\n", indent, i+1, oneLine(s.Title), s.Tag())
	if s.Why != "" {
		fmt.Fprintf(b, "%s#    %s\n", indent, oneLine(s.Why))
	}
	argv := Argv(s)
	if s.Remove == nil || len(s.Remove.Paths) <= 1 {
		b.WriteString(indent + quoteAll(argv, "") + tail + "\n")
		return
	}
	head := len(argv) - len(s.Remove.Paths)
	b.WriteString(indent + quoteAll(argv[:head], ""))
	for _, path := range s.Remove.Paths {
		b.WriteString(" \\\n" + indent + "  " + Quote(path))
	}
	b.WriteString(tail + "\n")
}
