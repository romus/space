#!/bin/sh
# Builds a throwaway home directory for trying space without touching real
# data. Use it like this:
#
#   hack/mkfixture.sh /tmp/space-home
#   SPACE_HOME=/tmp/space-home space
#
# SPACE_HOME makes space treat the folder as the home directory: it opens it
# instead of the current folder, looks for ~/.claude and ~/.codex inside it,
# and uses its own .Trash.
set -eu

home="${1:?usage: mkfixture.sh <directory>}"
case "$home" in
  /|"$HOME"|"$HOME"/) echo "refusing to use $home" >&2; exit 1 ;;
esac
mkdir -p "$home"
cd "$home"

# file <path> <megabytes> [days old]
file() {
  mkdir -p "$(dirname "$1")"
  dd if=/dev/urandom of="$1" bs=1048576 count="$2" 2>/dev/null
  if [ "${3:-0}" -gt 0 ]; then
    touch -a -m -t "$(date -v-"$3"d +%Y%m%d%H%M)" "$1"
  fi
}

file Movies/screen-recording-march.mov 24 241
file Movies/holiday.mp4 9 30
file Downloads/Xcode_16.1.xip 14 290
file Downloads/ubuntu-24.04-live-server-arm64.iso 11 380
file Downloads/notes.pdf 1 500
file Projects/ml/data/events-2025.parquet 18 120
file Projects/web/node_modules/big-lib/index.js 6
file Projects/web/src/app.js 1
file Projects/api/node_modules/dep/index.js 4
file Projects/indexer/.cache/lmdb/data.mdb 7 14
file Documents/thesis/draft.docx 5 2
file Pictures/2024/photo.raw 8 400
file .npm/_cacache/content/blob 6 60
file .cache/tool/index 3 20
file Library/Caches/com.example.app/cache.db 5 10
file Library/Application\ Support/Example/data.sqlite 4 3

# A sparse file: large on paper, small on disk.
mkdir -p VMs
dd if=/dev/zero of=VMs/disk.raw bs=1048576 count=4 2>/dev/null
truncate -s 2G VMs/disk.raw

# A hard link: must be counted once.
ln Movies/holiday.mp4 Movies/holiday-copy.mp4

# Claude Code: sessions per project, a memory folder and settings that must
# never be touched.
claude() { # <project dir> <cwd> <session id> <megabytes> <days old>
  dir=".claude/projects/$1"
  mkdir -p "$dir/memory" "$dir/$3/subagents" ".claude/file-history/$3"
  printf '{"type":"mode","sessionId":"%s"}\n{"type":"user","cwd":"%s","sessionId":"%s"}\n' "$3" "$2" "$3" > "$dir/$3.jsonl"
  dd if=/dev/urandom bs=1048576 count="$4" 2>/dev/null >> "$dir/$3.jsonl"
  echo "checkpoint" > ".claude/file-history/$3/a.txt"
  echo "agent log" > "$dir/$3/subagents/agent.jsonl"
  echo "remember this" > "$dir/memory/MEMORY.md"
  stamp="$(date -v-"$5"d +%Y%m%d%H%M)"
  touch -m -t "$stamp" "$dir/$3.jsonl"
}
claude -Users-me-work-api "$home/work/api" 11111111-aaaa-4aaa-8aaa-000000000001 6 90
claude -Users-me-work-api "$home/work/api" 11111111-aaaa-4aaa-8aaa-000000000002 4 45
claude -Users-me-work-api "$home/work/api" 11111111-aaaa-4aaa-8aaa-000000000003 2 1
claude -Users-me-work-indexer "$home/work/indexer" 22222222-bbbb-4bbb-8bbb-000000000001 5 47
claude -Users-me-Documents-thesis "$home/Documents/thesis" 33333333-cccc-4ccc-8ccc-000000000001 3 2
echo '{"theme":"dark"}' > .claude/settings.json
echo '# rules' > .claude/CLAUDE.md
mkdir -p .claude/skills/demo .claude/shell-snapshots
echo 'skill' > .claude/skills/demo/SKILL.md
file .claude/shell-snapshots/snapshot-old.sh 1 80
file .claude/shell-snapshots/snapshot-new.sh 1 1

# Codex: rollouts by date, config and login that must never be touched.
codex() { # <yyyy/mm/dd> <cwd> <id> <megabytes> <days old>
  dir=".codex/sessions/$1"
  mkdir -p "$dir"
  f="$dir/rollout-$3.jsonl"
  printf '{"type":"session_meta","payload":{"id":"%s","cwd":"%s"}}\n' "$3" "$2" > "$f"
  dd if=/dev/urandom bs=1048576 count="$4" 2>/dev/null >> "$f"
  touch -m -t "$(date -v-"$5"d +%Y%m%d%H%M)" "$f"
}
codex 2026/07/01 "$home/work/api" aaaa0001 3 140
codex 2026/09/15 "$home/work/api" aaaa0002 2 70
codex 2026/11/20 "$home/work/indexer" bbbb0001 2 5
echo 'model = "x"' > .codex/config.toml
echo '{"token":"secret"}' > .codex/auth.json
file .codex/log/codex-tui.log 3 100

echo "fixture ready: $home"
echo "try:  SPACE_HOME=$home space"
