# space

See what takes up your Mac's disk, and clean it safely, from the terminal.

```
space  ~/                                     Macintosh HD  ████████████████████░░░░  82 GB free of 494

 Folders  Files  Recipes 3                                   1.2M files · 6.1 s · by size on disk ↓
─━━━━━━━─────────────────────────────────────────────────────────────────────────────────────────────
    Library/        ██████████████████████████████    148.2 GB    46%
  ◆ .local/         ███████▊                           38.4 GB    12%   podman machine
    Developer/      ██████▍                            31.7 GB    10%
    Projects/       █████▋                             27.9 GB     9%   node_modules × 41 · 9.1 GB
  ○ Pictures/       █▉                                  9.6 GB     3%
  ● .npm/           ▊                                   4.2 GB     1%   npm cache
```

## Install

```
brew tap romus/tap
brew trust romus/tap
brew install space
```

Homebrew asks you to trust a third-party tap before it loads anything from it,
hence `brew trust`. The formula installs one binary for Apple silicon and
Intel, with shell completions. Or [build it yourself](#building).

## How it works

1. **Size is the only hierarchy.** Everything is sorted by the space it takes
   on disk (`st_blocks × 512`), so a sparse 100 GB VM image that really uses
   38 GB shows as 38 GB.
2. **Every row knows how it is removed.**

   | Mark | Meaning | What happens |
   | --- | --- | --- |
   | `·` | plain files | moved to the Trash |
   | `●` | cache | moved to the Trash; safe to delete with `D`, it will be rebuilt |
   | `◆` | owned by a recipe | the recipe runs the right commands in the right order |
   | `○` | protected folder | cannot be removed whole: open it and pick inside |

3. **Nothing runs without a plan.** The plan is exactly the commands that will
   run. Copy it as a shell script with `c`, or `space plan --script > clean.sh`.
4. **Reversible by default.** `d` moves to the Trash, `D` deletes for good and
   asks twice.

`○` marks the folders nobody means to lose in one keystroke: `Documents`,
`Desktop`, `Pictures`, `Music`, `Movies`, `Downloads`, `Library` and a few
more. The folder itself stays; everything inside it can be removed.

When something cannot be removed, space removes the rest and lists what stayed
behind with the reason, such as macOS not allowing it without Full Disk Access.

space shows the folder it is started in and never goes above it. Started above
your home folder (in `/` or `/Users`), it only looks: nothing can be removed.

## Recipes

A recipe handles what you cannot simply delete.

| Recipe | Owns | What it does |
| --- | --- | --- |
| Podman | `~/.local/share/containers` | removes the images, containers and volumes you pick, runs `fstrim` inside the machine so macOS learns which blocks are free, then stops the machine and starts it again |
| Claude Code | `~/.claude` | moves sessions you have not used for a while to the Trash, with their sub-agent logs and file checkpoints |
| Codex | `~/.codex` | moves old session rollouts to the Trash and deletes old logs |
| Git | the repositories under the folder you opened | deletes the branches that are merged, gone on origin or pushed, then forgets them in the reflog and compacts `.git`, the same steps in every repository, four repositories at a time |

The agent recipes work from an allow-list. Settings, `CLAUDE.md`, memory,
skills, plugins, `config.toml`, `auth.json` and the sqlite databases are never
touched, and neither is a session that is open right now.

### Git

Deleting a branch frees almost nothing by itself: its commits stay in `.git`
until the reflog forgets them and the repository is repacked. The Git recipe
does all of it in one plan.

- **What stays.** The main branch, the branch that is checked out, a branch
  checked out in a worktree, and the names in the rule (`main master develop`
  out of the box; edit it with `e`, patterns like `release/*` work). A branch
  with commits that exist nowhere else is never picked for you.
- **What is skipped.** A repository with uncommitted changes, a detached
  HEAD, or a rebase or merge in progress is left alone entirely.
- **Backups.** Branches that are not merged are saved to a bundle in
  `~/.local/state/space/git` first and kept for 30 days. The bundle holds
  what it saves, so that part of the space comes back when it goes. The log
  keeps the commit of every deleted branch.
- **Never touched.** Branches on the remote, stashes, tags, working files.
- **Origin.** The interface asks origin which branches it still has
  (`git ls-remote`, nothing is fetched) to find stale `origin/*` refs. The
  command line never uses the network.
- **Other people's repositories.** Every git command runs with the
  repository's hooks and file system monitor switched off, so looking at a
  folder never runs code that came with it.

Repositories in hidden folders, bare repositories and submodules are not
listed. Inside a repository, `space` goes straight to its branches.

> The Podman recipe is tested against recorded command output only. It has
> not yet been run against a real Podman machine.

## Usage

```
space                           open the folder you are in
space ~/Downloads               open another folder
space --path ~/Downloads        the same, as a flag (-p for short)
space --files                   start on the Files tab
space --theme system|light|dark

space recipe                    list the recipes found on this Mac
space recipe claude --dry-run   show a plan and change nothing
space recipe claude             show the plan, ask, then run it
space recipe git -p ~/work      the Git plan for the repositories under ~/work
space plan --script > clean.sh  every plan, as a shell script
```

### Keys

| Key | Does |
| --- | --- |
| `↑` `↓` `→` `←` | move, open a folder, go to the parent |
| `tab` | Folders · Files · Recipes |
| `space` | mark a row |
| `enter` | plan for the marked rows |
| `d` / `D` | plan: move to the Trash / delete forever |
| `r` | open the recipe of a `◆` row |
| `/` | search |
| `o` | reveal in Finder |
| `t` | theme: system → light → dark |
| `?` | every key |

## Themes

`system` follows the background of your terminal, asked through the terminal
itself; if the terminal does not answer, it follows the macOS appearance.
`light` and `dark` are fixed. space always paints its own background, so a
light theme stays readable in a dark terminal. To ask, it puts the terminal's
own background back for an instant, so the answer is the terminal's colour and
not the one space painted.

The theme is chosen by, in order: `--theme`, the `SPACE_THEME` variable,
`theme` in `~/.config/space/config.toml`, the last choice made with `t`.

```toml
# ~/.config/space/config.toml
theme = "system"
```

`NO_COLOR` is respected: the marks `· ● ◆ ○` carry the same meaning as colour.

## Files space writes

| Path | What |
| --- | --- |
| `~/.config/space/config.toml` | your settings; space only reads it |
| `~/.local/state/space/state.json` | the theme picked with `t`, pinned projects, hidden recipes, the Git rule |
| `~/.local/state/space/git/` | bundles of the branches the Git recipe deleted, kept 30 days |
| `~/.local/state/space/YYYY-MM-DD.log` | every plan that ran, with the output of each step |

## Full Disk Access

Without it macOS hides some folders (Mail, Messages, Safari data). space counts
what it could not read and says so. To see everything, add your terminal in
System Settings › Privacy & Security › Full Disk Access.

## Building

Requires Go 1.27 or later and macOS.

```
make build     # ./space
make install   # ~/.local/bin/space; PREFIX=/usr/local for a system-wide one
make uninstall # removes the binary, keeps settings and logs
make uninstall-all  # removes the binary, settings, state and logs
make test
make lint
make run       # try it on a throwaway home in /tmp/space-home
make dist      # dist/space-<version>-macos-universal.tar.gz, what Homebrew installs
```

`make install` and Homebrew put the binary in different places
(`~/.local/bin` and Homebrew's `bin`). With both, your `PATH` decides which
one runs; `make uninstall` removes the first.

`SPACE_HOME=<dir>` makes space treat a folder as the home directory: it opens
it instead of the current folder, looks for `.claude` and `.codex` inside it,
and uses its own `.Trash`.
`hack/mkfixture.sh <dir>` fills such a folder with test data.

## Layout

```
cmd/space           entry point
pkg/recipe          the recipe contract
internal/scan       parallel disk scanner
internal/classify   which path is removed how
internal/engine     runs plans: the only code that changes the disk
internal/recipes    trash, podman, git, agents (Claude Code, Codex)
internal/ui         theme, drawing kit, screens
internal/cli        commands
```

## License

MIT
