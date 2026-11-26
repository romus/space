//go:build darwin

// Package ui is the terminal interface of space.
package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/romus/space/internal/config"
	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/internal/ui/screens"
	"github.com/romus/space/internal/ui/theme"
	"github.com/romus/space/pkg/recipe"
)

// Options configure a run of the interface.
type Options struct {
	Root  string       // folder to scan
	Tab   int          // tab to open on
	Mode  theme.Mode   // system, light or dark
	Paths config.Paths // where config and state live
	State *config.State
}

const (
	marginX   = 2
	minWidth  = 80
	minHeight = 20
	tickEvery = 100 * time.Millisecond
)

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// App is the root model: a stack of screens, the theme, and the window.
type App struct {
	c        *screens.Ctx
	stack    []screens.Screen
	overview *screens.Overview
	init     tea.Cmd

	w, h     int
	termDark bool
	noColor  bool
}

// New builds the app.
func New(opt Options) *App {
	a := &App{termDark: theme.GuessDark()}
	a.c = &screens.Ctx{
		Paths: opt.Paths,
		State: opt.State,
		Root:  opt.Root,
		Mode:  opt.Mode,
		Env:   recipe.Env{Home: opt.Paths.Home, Run: engine.Reader{}, Now: time.Now},
	}
	a.applyTheme()
	a.overview, a.init = screens.NewOverview(a.c, opt.Tab)
	a.stack = []screens.Screen{a.overview}
	return a
}

// Run starts the interface and blocks until the user quits.
func Run(opt Options) error {
	_, err := tea.NewProgram(New(opt)).Run()
	return err
}

func (a *App) applyTheme() {
	th := theme.For(a.c.Mode, a.termDark)
	th.NoColor = a.noColor
	a.c.Theme = th
}

func (a *App) top() screens.Screen { return a.stack[len(a.stack)-1] }

func (a *App) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, tick(), a.init)
}

func (a *App) resize() {
	a.c.W = max(0, a.w-2*marginX)
	a.c.H = max(0, a.h-a.marginTop())
}

func (a *App) marginTop() int {
	if a.h >= 26 {
		return 1
	}
	return 0
}

// start begins the work of a screen that has just been placed.
func start(s screens.Screen) tea.Cmd {
	if st, ok := s.(screens.Starter); ok {
		return st.Start()
	}
	return nil
}

// broadcast sends a message to every screen on the stack.
func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for i, s := range a.stack {
		next, cmd := s.Update(msg)
		a.stack[i] = next
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		a.resize()
		return a, nil

	case tea.BackgroundColorMsg:
		// The terminal answered: this is what "system" means.
		a.termDark = msg.IsDark()
		a.applyTheme()
		return a, nil

	case tea.ColorProfileMsg:
		a.noColor = msg.Profile <= colorprofile.ASCII
		a.applyTheme()
		return a, nil

	case tea.FocusMsg:
		// The terminal may have switched profile while we were away.
		if a.c.Mode == theme.System {
			return a, tea.RequestBackgroundColor
		}
		return a, nil

	case tickMsg:
		a.c.Frame++
		return a, tea.Batch(tick(), a.broadcast(screens.TickMsg{}))

	case screens.PushMsg:
		a.stack = append(a.stack, msg.Screen)
		return a, start(msg.Screen)

	case screens.PopMsg:
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
		}
		return a, nil

	case screens.ReplaceMsg:
		a.stack[len(a.stack)-1] = msg.Screen
		if len(a.stack) == 1 {
			a.stack = []screens.Screen{a.overview, msg.Screen}
		}
		return a, start(msg.Screen)

	case screens.HomeMsg:
		a.stack = []screens.Screen{a.overview}
		if msg.Rescan {
			return a, a.overview.Rescan()
		}
		return a, nil

	case tea.KeyPressMsg:
		top := a.top()
		switch msg.String() {
		case "ctrl+c":
			if in, ok := top.(screens.Interrupter); ok {
				next, cmd := in.Interrupt()
				a.stack[len(a.stack)-1] = next
				return a, cmd
			}
			a.overview.Close()
			return a, tea.Quit
		case "t":
			if !top.Typing() {
				a.c.Mode = a.c.Mode.Next()
				a.applyTheme()
				a.c.State.Theme = string(a.c.Mode)
				_ = a.c.State.Save()
				next, cmd := top.Update(screens.ToastMsg{Text: "Theme: " + string(a.c.Mode)})
				a.stack[len(a.stack)-1] = next
				if a.c.Mode == theme.System {
					cmd = tea.Batch(cmd, tea.RequestBackgroundColor)
				}
				return a, cmd
			}
		}
		next, cmd := top.Update(msg)
		a.stack[len(a.stack)-1] = next
		return a, cmd
	}
	return a, a.broadcast(msg)
}

func (a *App) View() tea.View {
	th := a.c.Theme
	var content string
	switch {
	case a.w == 0 || a.h == 0:
		content = ""
	case a.w < minWidth || a.h < minHeight:
		content = a.tooSmall()
	default:
		side := th.Base().Render(strings.Repeat(" ", marginX))
		var b strings.Builder
		for i := 0; i < a.marginTop(); i++ {
			b.WriteString(kit.Blank(th, a.w))
			b.WriteByte('\n')
		}
		for i, line := range a.top().Lines() {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(side + line + side)
		}
		content = b.String()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.ReportFocus = true
	v.WindowTitle = "space — " + a.top().Title()
	if !a.noColor {
		// Ask the terminal to paint every cell, not only the ones we style.
		// Bubble Tea restores the terminal's own colours on exit.
		v.BackgroundColor = th.Bg
		v.ForegroundColor = th.Text
	}
	return v
}

func (a *App) tooSmall() string {
	th := a.c.Theme
	lines := make([]string, 0, a.h)
	msg := "space needs a window of at least 80 × 20"
	for i := 0; i < a.h; i++ {
		if i == a.h/2 {
			lines = append(lines, kit.Center(th, a.w, kit.Sp(th.S(th.Muted), msg)))
			continue
		}
		lines = append(lines, kit.Blank(th, a.w))
	}
	return strings.Join(lines, "\n")
}
