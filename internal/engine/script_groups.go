//go:build darwin

package engine

import (
	"fmt"
	"strings"

	"github.com/romus/space/pkg/recipe"
)

// A script does the groups one after another. It keeps the engine's rule
// that a command that fails ends its own group only: the steps of a group
// are chained with && inside braces, and a group that did not finish is
// remembered and reported at the end with a non-zero exit.
//
// Braces, not a subshell: inside "( … ) || …" the shell ignores set -e and
// would carry on after a failed command.

func scriptGroupsNote(b *strings.Builder, p recipe.Plan) {
	if p.Parallel > 1 {
		fmt.Fprintf(b, "# Here each %s is done in turn; space itself does %d at a time.\n", p.Noun(), p.Parallel)
	}
}

func scriptGroup(b *strings.Builder, p recipe.Plan, g Group) {
	fmt.Fprintf(b, "\n# ── %s ──\n{\n", oneLine(g.Name))
	for n, i := range g.Steps {
		tail := " &&"
		if n == len(g.Steps)-1 {
			tail = ""
		}
		scriptStep(b, p, i, "  ", tail)
	}
	fmt.Fprintf(b, "} || failed=\"$failed \"%s\n", Quote(oneLine(g.Name)))
}

func scriptGroupsEnd(b *strings.Builder) {
	b.WriteString("\n[ -z \"$failed\" ] || { echo \"not finished:$failed\" >&2; exit 1; }\n")
}
