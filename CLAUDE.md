# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

`space` is a macOS-only terminal UI (Go, Bubble Tea v2) that shows what takes up disk space and removes it safely. Every text the program shows or writes (UI, errors, CLI output, log, README, code comments) is in English.

## Commands

```
make build    # ./space (CGO off, version stamped through -ldflags)
make test     # go test ./...
make lint     # go vet -composites=false ./... and a gofmt check
make fmt      # gofmt -w .
make run      # build, create a throwaway home in /tmp/space-home, run space on it
make dist     # universal binary + dist/space-<version>-macos-universal.tar.gz + SHA256
make formula  # render dist/space.rb for that archive (FORMULA_URL=file://… for a local tap)
```

- One test: `go test ./internal/engine -run 'TestName' -v`
- Rewrite the golden screens after an intended UI change: `go test ./internal/ui/screens -update`
- Compare the scanner with `du` on a real folder (read-only, skipped otherwise): `SPACE_REAL_SCAN=$HOME go test ./internal/scan -run TestRealScan -v`

Requires Go 1.27+. Almost every package carries `//go:build darwin`; new files that touch syscalls, the engine or the UI need the same tag.

### Never try removal on the real home

`SPACE_HOME=<dir>` replaces the home directory entirely: space opens it by default, looks for `.claude` and `.codex` inside it, ignores `XDG_*`, keeps config and state under it, and moves files to `<dir>/.Trash` instead of calling `/usr/bin/trash`. `hack/mkfixture.sh <dir>` fills such a folder with test data (sparse file, hard link, Claude Code and Codex sessions). Use both (or `make run`) for any manual run that deletes; tests do the same with `t.Setenv("SPACE_HOME", t.TempDir())`.

## Architecture

The flow is always: **find → plan → show → run**. Nothing changes the disk without a `recipe.Plan` that was shown first.

- `pkg/recipe` is the contract. A `Recipe` only detects, inspects and returns a `Plan` made of `Step`s; it never deletes. A step is either `Cmd` (argv) or `Remove` (paths the engine deletes itself, inside `Root`). The step's tag (READ-ONLY / SAFE / REVERSIBLE / IRREVERSIBLE) is derived from its flags and drives the double confirmation.
- `internal/engine` is the only code that changes the disk. `Engine.Run` executes steps in order, streams output as events, writes the daily log, and measures free space before and after. Its removal checks are the safety net for everything above it: the root may not be `/` or anything above the home directory, each path must resolve strictly inside the root, and a path whose inode changed since `engine.Prepare` is refused. Call `engine.Prepare(&plan)` whenever a plan is built or rebuilt before showing it. A path that cannot be removed becomes a `Failure` and does not stop the rest; only a failing command or a refused root fails the step and ends the run.
- `engine.Script` and `engine.Display` render the same steps as a shell script or one line, so the plan screen, `--dry-run`, "copy as script" and the log always show what will run. `engine.Reader` is the read-only command runner handed to recipes through `recipe.Env`.
- `internal/scan` walks the tree in parallel. Size is always `st_blocks × 512` (what is taken on disk, not file length); hard links count once, other volumes are not entered, iCloud placeholders are not downloaded. Only directories stay in memory; files are listed on demand and the largest are kept in a heap.
- `internal/classify` decides how a path is removed: plain (Trash), cache (deleted), special (owned by a detected recipe, including the parent folders that lead to it), or protected (`○`: the folder cannot be removed whole, everything inside it can). Recipes are detected before the classifier is built because it needs the folders they own.
- `internal/recipes`: `podman` and `agents` (Claude Code, Codex) register themselves in `init()` through `recipe.Register`. `trash` is not a registered recipe: it is the plan builder behind marked rows on the overview. The agent recipes work from an allow-list of what may be offered; anything unrecognised is never touched, and sessions that are open are locked.
- `internal/cli` (cobra): the root command starts the UI; `recipe`, `plan` and `version` work without it and share `planFor` for the default selection.
- `internal/config`: `config.toml` is read only; `state.json` (theme picked with `t`, keep window, pinned projects, hidden recipes) is what space writes.

### Adding a recipe

Implement `recipe.Recipe`, register it in `init()`, and keep the `recipe.Env` given to `Detect` for later calls (tests swap `Env.Run` for recorded output and `Env.Now` for a fixed time). The package must be imported for the registration to happen (see the imports in `internal/cli/cli.go`), and it needs a screen wired in `Overview.openRecipe` in `internal/ui/screens/overview.go`; without one the UI says "No screen for this recipe yet". Implement `recipe.Measurer` when the recipe has its own number to track, like the Podman machine disk.

### UI

- `internal/ui/app.go` is the root model: a stack of screens with the overview at the bottom. Screens never touch the stack; they return `PushMsg`, `PopMsg`, `ReplaceMsg` or `HomeMsg`. The app handles `ctrl+c` and `t` (theme) itself and asks `Typing()` before treating a letter as a shortcut.
- A screen's `Lines()` must return exactly `Ctx.H` lines, each exactly `Ctx.W` columns wide. The `text` helper in the screen tests fails on any other shape, so build lines with `internal/ui/kit` (`Row`, `Fit`, `Cell`, `Frame`) rather than by hand.
- A screen that waits for background work implements `Starter`; the app calls `Start` only after the screen is on the stack. The run screen depends on this: reading engine events earlier races with the screen switch and the run looks stuck.
- Colours come only from `theme.Theme` fields through `t.Base()`, `t.S()`, `t.Sel()`, `t.Soft()`, never hex values in screens. These helpers also carry the window background and the `NO_COLOR` path (reverse video for the selection; the glyphs `· ● ◆ ○` carry the meaning).
- Theme `system` means the terminal's own background, asked over OSC 11. space paints the background itself, so the probe in `app.go` resets it, asks, and paints again in one write. The fallback is the macOS appearance, then dark. Precedence of the setting: `--theme`, `SPACE_THEME`, `config.toml`, `state.json`.
- Screen order for any removal: Overview or a recipe screen → `Plan` → `Run` → `Done`, all carrying one `PlanSpec` (`Rebuild` re-plans after a toggle, `Measure` reads the recipe's own number). Editing a command on the plan screen drops `Rebuild`: the plan is no longer the recipe's.

## Versioning and release

- The version is the git tag and lives nowhere else. The Makefile strips the leading `v` (`git describe`), so tag `v1.2.3` builds `space 1.2.3`; the linker stamps it into `main.version`.
- Pushing a `v*` tag runs `.github/workflows/release.yml`: lint and test, `make dist` (arm64 and amd64 joined with `lipo`, archived as `space-<version>-macos-universal.tar.gz`), GitHub release, an install smoke test through a temporary tap, then a pull request against the shared tap `romus/homebrew-tap` through its bump action (`romus/homebrew-tap/.github/actions/bump`, secret `HOMEBREW_TAP_PAT`).
- The formula template is `scripts/render_formula.sh`; `Formula/space.rb` in the tap is never edited by hand. It has no `version` line: Homebrew reads the version from the archive name, so that name is part of the contract.
- The formula installs a prebuilt binary, so nothing is compiled on the user's Mac and Homebrew does not quarantine it. `make dist` signs both slices ad hoc (`dev.romus.space`): the Go linker signs only arm64, and `codesign --verify` fails on a file with an unsigned slice.
