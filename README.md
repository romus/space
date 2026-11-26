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
  ○ Pictures/       █▉                                  9.6 GB     3%   Photos · view only
  ● .npm/           ▊                                   4.2 GB     1%   npm cache
```

## How it works

1. **Size is the only hierarchy.** Everything is sorted by the space it takes
   on disk (`st_blocks × 512`), so a sparse 100 GB VM image that really uses
   38 GB shows as 38 GB.
2. **Every row knows how it is removed.**

   | Mark | Meaning | What happens |
   | --- | --- | --- |
   | `·` | plain files | moved to the Trash |
   | `●` | cache | deleted; it will be rebuilt |
   | `◆` | owned by a recipe | the recipe runs the right commands in the right order |
   | `○` | personal data | view only, never removed |

3. **Nothing runs without a plan.** The plan is exactly the commands that will
   run. Copy it as a shell script with `c`, or `space plan --script > clean.sh`.
4. **Reversible by default.** `d` moves to the Trash, `D` deletes for good and
   asks twice.

## Recipes

A recipe handles what you cannot simply delete.

| Recipe | Owns | What it does |
| --- | --- | --- |
| Podman | `~/.local/share/containers` | removes the images, containers and volumes you pick, runs `fstrim` inside the machine so macOS learns which blocks are free, then stops the machine |
| Claude Code | `~/.claude` | moves sessions you have not used for a while to the Trash, with their sub-agent logs and file checkpoints |
| Codex | `~/.codex` | moves old session rollouts to the Trash and deletes old logs |

The agent recipes work from an allow-list. Settings, `CLAUDE.md`, memory,
skills, plugins, `config.toml`, `auth.json` and the sqlite databases are never
touched, and neither is a session that is open right now.

> The Podman recipe is tested against recorded command output only. It has
> not yet been run against a real Podman machine.

## Usage

```
space [path]                    open the interface, on ~ by default
space --files                   start on the Files tab
space --theme system|light|dark

space recipe                    list the recipes found on this Mac
space recipe claude --dry-run   show a plan and change nothing
space recipe claude             show the plan, ask, then run it
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
light theme stays readable in a dark terminal.

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
| `~/.local/state/space/state.json` | the theme picked with `t`, pinned projects, hidden recipes |
| `~/.local/state/space/YYYY-MM-DD.log` | every plan that ran, with the output of each step |

## Full Disk Access

Without it macOS hides some folders (Mail, Messages, Safari data). space counts
what it could not read and says so. To see everything, add your terminal in
System Settings › Privacy & Security › Full Disk Access.

## Building

Requires Go 1.27 or later and macOS.

```
make build     # ./space
make test
make lint
make run       # try it on a throwaway home in /tmp/space-home
```

`SPACE_HOME=<dir>` makes space treat a folder as the home directory: it scans
it, looks for `.claude` and `.codex` inside it, and uses its own `.Trash`.
`hack/mkfixture.sh <dir>` fills such a folder with test data.

## Layout

```
cmd/space           entry point
pkg/recipe          the recipe contract
internal/scan       parallel disk scanner
internal/classify   which path is removed how
internal/engine     runs plans: the only code that changes the disk
internal/recipes    trash, podman, agents (Claude Code, Codex)
internal/ui         theme, drawing kit, screens
internal/cli        commands
```

## License

MIT
