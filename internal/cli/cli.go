//go:build darwin

// Package cli is the command line of space: the interface by default, plus
// a few commands that work without a terminal UI.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/romus/space/internal/config"
	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/recipes/agents"
	_ "github.com/romus/space/internal/recipes/podman" // registers the recipe
	"github.com/romus/space/internal/ui"
	"github.com/romus/space/internal/ui/screens"
	"github.com/romus/space/internal/ui/theme"
	"github.com/romus/space/pkg/recipe"
)

// Build information, set by the linker.
type Build struct{ Version, Commit, Date string }

type app struct {
	info  Build
	paths config.Paths
	cfg   config.Config
	state *config.State
	env   recipe.Env
}

func (a *app) load() error {
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return fmt.Errorf("config.toml: %w", err)
	}
	a.paths, a.cfg, a.state = paths, cfg, config.LoadState(paths)
	a.env = recipe.Env{Home: paths.Home, Run: engine.Reader{}, Now: time.Now}
	return nil
}

// themeMode picks the theme: the flag, then SPACE_THEME, then config.toml,
// then the last choice made with t, then the system.
func (a *app) themeMode(flag string) (theme.Mode, error) {
	for _, v := range []string{flag, os.Getenv("SPACE_THEME"), a.cfg.Theme, a.state.Theme} {
		if v != "" {
			return theme.ParseMode(v)
		}
	}
	return theme.System, nil
}

// resolveRoot picks the folder to open: the --path flag or the argument,
// else the folder space was started in.
//
// Under SPACE_HOME the default is the sandbox itself, not the current
// folder: a sandboxed run must never wander into real data on its own.
func resolveRoot(arg, flag string, paths config.Paths) (string, error) {
	if arg != "" && flag != "" {
		return "", errors.New("the folder is given twice: use either a path or --path, not both")
	}
	dir := flag
	if dir == "" {
		dir = arg
	}
	switch {
	case dir == "~":
		dir = paths.Home
	case strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(paths.Home, dir[2:])
	case dir == "" && config.Sandboxed():
		dir = paths.Home
	case dir == "":
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cannot tell which folder this is: %w", err)
		}
		dir = wd
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	st, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("%s does not exist", dir)
	case err != nil:
		return "", err
	case !st.IsDir():
		return "", fmt.Errorf("%s is not a folder", dir)
	}
	return dir, nil
}

// Execute runs the command line and returns the exit code.
func Execute(build Build) int {
	a := &app{info: build}
	var (
		themeFlag string
		pathFlag  string
		files     bool
		recipes   bool
	)
	root := &cobra.Command{
		Use:   "space [path]",
		Short: "See what takes up your Mac's disk and clean it safely",
		Long: "space shows folders and files by the room they take on disk, and removes them the right way:\n" +
			"plain files go to the Trash, caches are deleted, and things that cannot simply be deleted —\n" +
			"a Podman machine, Claude Code or Codex sessions — get a recipe with the right order of commands.\n\n" +
			"Nothing runs without a plan, and the plan is exactly what will run.\n\n" +
			"space opens the folder you start it in. Give a path to open another one:\n" +
			"  space ~/Downloads\n" +
			"  space --path ~/Downloads",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       build.Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			mode, err := a.themeMode(themeFlag)
			if err != nil {
				return err
			}
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			dir, err := resolveRoot(arg, pathFlag, a.paths)
			if err != nil {
				return err
			}
			if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
				return errors.New("space needs a terminal. Without one, try: space plan --script")
			}
			tab := screens.TabFolders
			switch {
			case files:
				tab = screens.TabFiles
			case recipes:
				tab = screens.TabRecipes
			}
			return ui.Run(ui.Options{Root: dir, Tab: tab, Mode: mode, Paths: a.paths, State: a.state})
		},
	}
	root.SetVersionTemplate("space {{.Version}}\n")
	root.Flags().StringVar(&themeFlag, "theme", "", "colour theme: system, light or dark")
	root.Flags().StringVarP(&pathFlag, "path", "p", "", "folder to open (default: the current folder)")
	root.Flags().BoolVar(&files, "files", false, "open on the Files tab")
	root.Flags().BoolVar(&recipes, "recipes", false, "open on the Recipes tab")
	root.AddCommand(a.recipeCmd(), a.planCmd(), a.versionCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "space:", err)
		return 1
	}
	return 0
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "space %s\n", a.info.Version)
			if a.info.Commit != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "commit %s, built %s\n", a.info.Commit, a.info.Date)
			}
		},
	}
}

// detected returns the recipes found on this machine.
func (a *app) detected(ctx context.Context) []recipe.Recipe {
	var out []recipe.Recipe
	for _, r := range recipe.All() {
		if r.Detect(ctx, a.env) {
			out = append(out, r)
		}
	}
	return out
}

func (a *app) find(ctx context.Context, id string) (recipe.Recipe, error) {
	var ids []string
	for _, r := range a.detected(ctx) {
		if r.ID() == id {
			return r, nil
		}
		ids = append(ids, r.ID())
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no recipe %q: nothing on this Mac needs a recipe", id)
	}
	return nil, fmt.Errorf("no recipe %q here. Found: %s", id, strings.Join(ids, ", "))
}

type planOpts struct {
	keepDays  int
	permanent bool
}

// build inspects a recipe and plans its default selection.
func (a *app) planFor(ctx context.Context, r recipe.Recipe, o planOpts) (recipe.Plan, []recipe.Item, error) {
	items, err := r.Inspect(ctx)
	if err != nil {
		return recipe.Plan{}, nil, err
	}
	keep := o.keepDays
	if keep <= 0 {
		keep = a.state.KeepDays
	}
	if keep <= 0 {
		keep = agents.DefaultKeepDays
	}
	opt := recipe.Options{KeepDays: keep, Pinned: a.state.PinnedSet(r.ID()), Permanent: o.permanent}
	sel := recipe.DefaultSelection(items)
	if _, ok := r.(agents.Source); ok {
		sel = agents.Select(items, keep, opt.Pinned, nil, a.env.Now())
	}
	plan, err := r.Plan(sel, opt)
	if err != nil {
		return recipe.Plan{}, items, err
	}
	engine.Prepare(&plan)
	return plan, items, nil
}

func (a *app) recipeCmd() *cobra.Command {
	var (
		dryRun, yes bool
		o           planOpts
	)
	cmd := &cobra.Command{
		Use:   "recipe [id]",
		Short: "List recipes, or plan and run one without the interface",
		Long: "Without an id, lists the recipes found on this Mac.\n" +
			"With an id, shows the plan for the recipe's default selection and, unless --dry-run is given,\n" +
			"asks before running it.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				return a.listRecipes(ctx, out)
			}
			r, err := a.find(ctx, args[0])
			if err != nil {
				return err
			}
			plan, _, err := a.planFor(ctx, r, o)
			if err != nil {
				return err
			}
			printPlan(out, plan, a.paths.Home)
			if dryRun {
				fmt.Fprintln(out, "\nDry run: nothing was changed.")
				return nil
			}
			if !yes {
				if !term.IsTerminal(os.Stdin.Fd()) {
					return errors.New("refusing to run without confirmation: pass --yes, or --dry-run to only look")
				}
				fmt.Fprintf(out, "\nRun %s? [y/N] ", humanize.Plural(len(plan.Steps), "step"))
				answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
					fmt.Fprintln(out, "Nothing was changed.")
					return nil
				}
			}
			return a.run(ctx, out, r, plan)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the plan and change nothing")
	cmd.Flags().BoolVar(&yes, "yes", false, "run without asking")
	cmd.Flags().IntVar(&o.keepDays, "keep-days", 0, "AI agents: keep sessions used in the last N days (default 30)")
	cmd.Flags().BoolVar(&o.permanent, "permanent", false, "delete for good instead of moving to the Trash")
	return cmd
}

func (a *app) listRecipes(ctx context.Context, out io.Writer) error {
	list := a.detected(ctx)
	if len(list) == 0 {
		fmt.Fprintln(out, "Nothing on this Mac needs a recipe.")
		return nil
	}
	for _, r := range list {
		info := r.Info()
		items, err := r.Inspect(ctx)
		found := r.Summary(items)
		if err != nil {
			found = "error: " + err.Error()
		}
		fmt.Fprintf(out, "%-8s %-12s %s\n", r.ID(), info.Name, humanize.Tilde(info.Path, a.paths.Home))
		fmt.Fprintf(out, "%-8s %s\n", "", found)
	}
	fmt.Fprintln(out, "\nSee a plan with: space recipe <id> --dry-run")
	return nil
}

func printPlan(out io.Writer, p recipe.Plan, home string) {
	fmt.Fprintf(out, "%s — %s · ≈ %s · nothing has run yet\n", p.Title,
		humanize.Plural(len(p.Steps), "step"), humanize.Duration(time.Duration(p.Seconds)*time.Second))
	for i, s := range p.Steps {
		fmt.Fprintf(out, "\n%2d  %-58s %s\n", i+1, s.Title, s.Tag())
		fmt.Fprintf(out, "    $ %s\n", engine.Display(s, home))
		if s.Why != "" {
			fmt.Fprintf(out, "    %s\n", s.Why)
		}
	}
	switch {
	case p.Metric != nil:
		fmt.Fprintf(out, "\n%s: %s → ≈ %s\n", p.Metric.Label, humanize.Bytes(p.Metric.Before), humanize.Bytes(p.Metric.After))
	case p.Reclaim > 0 || p.ToTrash > 0:
		fmt.Fprintf(out, "\nFrees ≈ %s, moves %s to the Trash.\n", humanize.Bytes(p.Reclaim), humanize.Bytes(p.ToTrash))
	}
}

// run executes a plan with plain text progress.
func (a *app) run(ctx context.Context, out io.Writer, r recipe.Recipe, plan recipe.Plan) error {
	eng := &engine.Engine{Home: a.paths.Home, Volume: a.paths.Home, LogPath: a.paths.LogFile(a.env.Now())}
	if m, ok := r.(recipe.Measurer); ok {
		eng.Measure = m.Measure
	}
	fmt.Fprintln(out)
	res := eng.Run(ctx, plan, func(ev engine.Event) {
		switch ev := ev.(type) {
		case engine.StepStarted:
			fmt.Fprintf(out, "→ %d/%d %s\n", ev.Index+1, len(plan.Steps), plan.Steps[ev.Index].Title)
		case engine.StepOutput:
			fmt.Fprintf(out, "    %s\n", ev.Line)
		case engine.StepFinished:
			if ev.Result.Err != nil {
				fmt.Fprintf(out, "  ✗ %v\n", ev.Result.Err)
			} else {
				fmt.Fprintf(out, "  ✓ %s\n", humanize.Duration(ev.Result.Elapsed))
			}
		}
	})
	fmt.Fprintln(out)
	if res.Metric != nil {
		fmt.Fprintf(out, "%s: %s → %s\n", res.Metric.Label, humanize.Bytes(res.Metric.Before), humanize.Bytes(res.Metric.After))
	}
	fmt.Fprintf(out, "Free space: %s → %s (reclaimed %s)\n", humanize.Bytes(res.FreeBefore),
		humanize.Bytes(res.FreeAfter), humanize.Bytes(res.Reclaimed()))
	if res.Trashed > 0 {
		fmt.Fprintf(out, "%s moved to the Trash: space comes back when you empty it.\n", humanize.Bytes(res.Trashed))
	}
	failures := res.Failures()
	if n := len(failures); n > 0 {
		noun := "items"
		if n == 1 {
			noun = "item"
		}
		fmt.Fprintf(out, "%d %s could not be removed:\n", n, noun)
		for _, f := range failures {
			fmt.Fprintf(out, "  %s — %s\n", humanize.Tilde(f.Path, a.paths.Home), f.Reason)
		}
	}
	fmt.Fprintf(out, "Log: %s\n", humanize.Tilde(res.LogPath, a.paths.Home))
	if res.Failed >= 0 {
		return fmt.Errorf("step %d failed: %w", res.Failed+1, res.Err)
	}
	if len(failures) > 0 {
		return errors.New("some items could not be removed")
	}
	return nil
}

func (a *app) planCmd() *cobra.Command {
	var (
		script bool
		o      planOpts
	)
	cmd := &cobra.Command{
		Use:   "plan [recipe...]",
		Short: "Print the plan of every recipe, as text or as a shell script",
		Long: "Builds the plan each recipe would run for its default selection and prints it.\n" +
			"Nothing is changed. With --script the output is a shell script: space plan --script > clean.sh",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			var list []recipe.Recipe
			if len(args) == 0 {
				list = a.detected(ctx)
			}
			for _, id := range args {
				r, err := a.find(ctx, id)
				if err != nil {
					return err
				}
				list = append(list, r)
			}
			if len(list) == 0 {
				return errors.New("nothing on this Mac needs a recipe")
			}
			printed := 0
			for _, r := range list {
				plan, _, err := a.planFor(ctx, r, o)
				if err != nil {
					// A recipe with nothing to do is not a failure of the others.
					if script {
						fmt.Fprintf(out, "# %s: skipped — %v\n\n", r.Info().Name, err)
					} else {
						fmt.Fprintf(out, "%s — skipped: %v\n\n", r.Info().Name, err)
					}
					continue
				}
				if printed > 0 {
					fmt.Fprintln(out)
				}
				if script {
					text := engine.Script(plan, a.env.Now())
					if printed > 0 {
						text = strings.TrimPrefix(text, "#!/bin/sh\n")
					}
					fmt.Fprint(out, text)
				} else {
					printPlan(out, plan, a.paths.Home)
				}
				printed++
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&script, "script", false, "print a shell script instead of a description")
	cmd.Flags().IntVar(&o.keepDays, "keep-days", 0, "AI agents: keep sessions used in the last N days (default 30)")
	cmd.Flags().BoolVar(&o.permanent, "permanent", false, "delete for good instead of moving to the Trash")
	return cmd
}
