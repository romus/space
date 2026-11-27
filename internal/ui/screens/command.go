//go:build darwin

package screens

import (
	"image/color"
	"strings"

	"github.com/romus/space/pkg/recipe"
)

// splitCommand splits a command line into words, honouring quotes.
func splitCommand(s string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	flush := func() {
		if has {
			out = append(out, cur.String())
			cur.Reset()
			has = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(rs) {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, has = r, true
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			has = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return out
}

func tagColor(c *Ctx, s recipe.Step) color.Color {
	t := c.Theme
	switch s.Tag() {
	case "IRREVERSIBLE":
		return t.Danger
	case "READ-ONLY":
		return t.Muted
	}
	return t.Safe
}
