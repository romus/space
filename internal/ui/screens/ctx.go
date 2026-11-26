//go:build darwin

// Package screens holds every screen of space. A screen draws lines exactly
// as wide and as tall as the context says, and talks to the app through
// navigation messages.
package screens

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/config"
	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/internal/ui/theme"
	"github.com/romus/space/pkg/recipe"
)

// Ctx is shared by every screen.
type Ctx struct {
	Theme *theme.Theme
	W, H  int // size of the content area
	Frame int // advances with every tick, drives spinners

	Paths config.Paths
	State *config.State
	Env   recipe.Env
	Root  string // folder being scanned
	Mode  theme.Mode
}

// Home is the home directory.
func (c *Ctx) Home() string { return c.Paths.Home }

// Tilde shortens a path for display.
func (c *Ctx) Tilde(path string) string { return humanize.Tilde(path, c.Paths.Home) }

// Now is the current time.
func (c *Ctx) Now() time.Time { return c.Env.Now() }

// NewEngine builds an engine for a plan.
func (c *Ctx) NewEngine(measure func(context.Context) (string, int64, bool)) *engine.Engine {
	return &engine.Engine{
		Home:    c.Paths.Home,
		Volume:  c.Root,
		LogPath: c.Paths.LogFile(c.Now()),
		Measure: measure,
	}
}

// Screen is one screen on the stack.
type Screen interface {
	Update(msg tea.Msg) (Screen, tea.Cmd)
	// Lines returns exactly c.H lines, each exactly c.W columns wide.
	Lines() []string
	// Title goes to the terminal window title: "space — podman".
	Title() string
	// Typing reports that the screen is reading text, so letters are not
	// shortcuts right now.
	Typing() bool
}

// Interrupter is implemented by screens that handle ctrl+c themselves.
type Interrupter interface {
	Interrupt() (Screen, tea.Cmd)
}

// Navigation messages, handled by the app.
type (
	// PushMsg opens a screen on top of the current one.
	PushMsg struct{ Screen Screen }
	// PopMsg goes back one screen.
	PopMsg struct{}
	// ReplaceMsg swaps the current screen for another.
	ReplaceMsg struct{ Screen Screen }
	// HomeMsg returns to the overview. With Rescan the disk is read again.
	HomeMsg struct{ Rescan bool }
	// TickMsg arrives several times a second.
	TickMsg struct{}
	// ToastMsg shows a short note on the current screen.
	ToastMsg struct{ Text string }
)

func push(s Screen) tea.Cmd { return func() tea.Msg { return PushMsg{s} } }
func pop() tea.Cmd          { return func() tea.Msg { return PopMsg{} } }
func replace(s Screen) tea.Cmd {
	return func() tea.Msg { return ReplaceMsg{s} }
}
func goHome(rescan bool) tea.Cmd { return func() tea.Msg { return HomeMsg{Rescan: rescan} } }

// RecipeState is a detected recipe and what its last inspection found.
type RecipeState struct {
	R      recipe.Recipe
	Items  []recipe.Item
	Err    error
	Loaded bool
}

// Reclaim is the size of the recipe's default selection.
func (rs *RecipeState) Reclaim() int64 {
	return recipe.Sum(recipe.DefaultSelection(rs.Items))
}

// toast is a short note that fades after a moment.
type toast struct {
	text  string
	until time.Time
}

func (t *toast) set(text string) {
	t.text = text
	t.until = time.Now().Add(3 * time.Second)
}

func (t *toast) get() string {
	if t.text == "" || time.Now().After(t.until) {
		return ""
	}
	return t.text
}

// header draws the top line: the name, a breadcrumb, and a right side.
func header(c *Ctx, crumbs []kit.Span, right []kit.Span) string {
	t := c.Theme
	left := []kit.Span{kit.Sp(t.S(t.Strong).Bold(true), "space")}
	if len(crumbs) > 0 {
		left = append(left, kit.Sp(t.Base(), "  "))
		left = append(left, crumbs...)
	}
	return kit.Row(c.W, t.Base(), left, right)
}

// diskGauge is the right side of the overview header.
func diskGauge(c *Ctx, v scan.Volume) []kit.Span {
	t := c.Theme
	if v.Total == 0 {
		return nil
	}
	return []kit.Span{
		kit.Sp(t.S(t.Muted), v.Name),
		kit.Sp(t.Base(), "  "),
		kit.Sp(t.S(t.Subtle), kit.Gauge(v.Used(), v.Total, 24)),
		kit.Sp(t.Base(), "  "),
		kit.Sp(t.S(t.Strong), humanize.GB(v.Free)+" GB"),
		kit.Sp(t.S(t.Muted), " free of "+humanize.GB(v.Total)),
	}
}

// openLog shows the log in a pager.
func openLog(path string) tea.Cmd {
	cmd := exec.Command("less", "+G", path)
	return tea.ExecProcess(cmd, func(error) tea.Msg { return nil })
}

// reveal shows a file in Finder.
func reveal(path string) tea.Cmd {
	return func() tea.Msg {
		_ = exec.Command("open", "-R", path).Start()
		return nil
	}
}

// copyText puts text on the clipboard, by escape sequence and by pbcopy.
func copyText(text string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(text), func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
		return nil
	})
}

// keyOf normalises a key press to a short name.
func keyOf(msg tea.KeyPressMsg) string {
	s := msg.String()
	switch s {
	case "shift+d":
		return "D"
	case "shift+g":
		return "G"
	case " ":
		return "space"
	}
	return s
}

// wrap breaks text into lines of at most w columns.
func wrap(text string, w int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case kit.Width(line)+1+kit.Width(word) <= w:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
