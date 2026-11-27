//go:build darwin

package screens

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/recipes/git"
)

// startRule begins editing the rule: the names of the branches that stay.
func (g *Git) startRule() {
	g.editing = true
	g.input = strings.Join(g.keep, " ")
}

// ruleKey handles typing the rule. Enter saves it for every repository,
// now and next time; an empty rule goes back to the one out of the box.
func (g *Git) ruleKey(k string, key tea.KeyPressMsg) {
	switch k {
	case "esc":
		g.editing = false
	case "enter":
		g.editing = false
		rule := git.ParseKeep(g.input)
		g.c.State.GitKeep = rule
		if len(rule) == 0 {
			rule = git.DefaultKeep
		}
		g.keep = rule
		if err := g.c.State.Save(); err != nil {
			g.toast.set("The rule is set, but could not be saved: " + err.Error())
		}
		for _, repo := range g.repos {
			if repo.Skip == "" {
				g.estimate(repo.Path)
			}
		}
	case "backspace":
		if r := []rune(g.input); len(r) > 0 {
			g.input = string(r[:len(r)-1])
		}
	default:
		if key.Text != "" {
			g.input += key.Text
		}
	}
}
