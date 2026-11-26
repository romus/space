#!/usr/bin/env bash
# Renders the Homebrew formula for a release.
#
#   scripts/render_formula.sh --version 1.2.3 --sha256 <sha> --output dist/space.rb
#
# --url overrides the download URL (the release workflow points it at a local
# file:// archive for its smoke test). The copy in romus/homebrew-tap comes
# from this script and is never edited by hand.
set -euo pipefail

VERSION=""
SHA256=""
OUTPUT=""
URL=""
REPO="${GITHUB_REPOSITORY:-romus/space}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --sha256) SHA256="${2:-}"; shift 2 ;;
    --output) OUTPUT="${2:-}"; shift 2 ;;
    --url) URL="${2:-}"; shift 2 ;;
    --repo) REPO="${2:-}"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

[ -n "$VERSION" ] || { echo "--version is required" >&2; exit 1; }
[ -n "$SHA256" ] || { echo "--sha256 is required" >&2; exit 1; }
[ -n "$OUTPUT" ] || { echo "--output is required" >&2; exit 1; }

case "$VERSION" in
  v*) echo "--version takes the version without the leading v, got '$VERSION'" >&2; exit 1 ;;
esac
if ! [[ "$SHA256" =~ ^[0-9a-f]{64}$ ]]; then
  echo "--sha256 must be 64 lowercase hex digits, got '$SHA256'" >&2
  exit 1
fi

HOMEPAGE="https://github.com/${REPO}"
if [ -z "$URL" ]; then
  URL="${HOMEPAGE}/releases/download/v${VERSION}/space-${VERSION}-macos-universal.tar.gz"
fi

# There is no version line on purpose: Homebrew reads the version from the
# archive name, and `brew audit` rejects a line that only repeats it.
mkdir -p "$(dirname "$OUTPUT")"
cat > "$OUTPUT" <<EOF
class Space < Formula
  desc "See what takes up your Mac's disk, and clean it safely, from the terminal"
  homepage "${HOMEPAGE}"
  url "${URL}"
  sha256 "${SHA256}"
  license "MIT"

  depends_on :macos

  def install
    bin.install "space"
    generate_completions_from_executable(bin/"space", "completion")
  end

  test do
    assert_match "space #{version}", shell_output("#{bin}/space version")
  end
end
EOF

echo "Wrote $OUTPUT"
