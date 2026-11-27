//go:build darwin

package git

import (
	"path"
	"strings"

	"github.com/romus/space/pkg/recipe"
)

// DefaultKeep is the rule out of the box: branches with these names stay.
// The main branch, the branch that is checked out and anything with commits
// that exist nowhere else stay whatever the rule says.
var DefaultKeep = []string{"main", "master", "develop"}

// ParseKeep reads a rule typed by the user: names or patterns such as
// release/*, separated by spaces or commas.
func ParseKeep(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
}

// Kept reports whether the rule keeps a branch of this name.
func Kept(name string, keep []string) bool {
	for _, pattern := range keep {
		if pattern == name {
			return true
		}
		if ok, err := path.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}

// MarkKey names an item in the marks the user set by hand.
func MarkKey(it recipe.Item) string { return it.Group + "\x00" + it.ID }

// Chosen reports whether an item is taken: by the user's own mark when
// there is one, else by what is safe to take. Something locked, or a branch
// the rule keeps, is never taken.
func Chosen(it recipe.Item, keep []string, marks map[string]bool) bool {
	kind := it.Meta[MetaKind]
	if it.Locked != "" || (kind == KindBranch && Kept(it.Title, keep)) {
		return false
	}
	if v, ok := marks[MarkKey(it)]; ok {
		return v
	}
	switch kind {
	case KindRepo:
		return true
	case KindBranch:
		return it.Meta[MetaWhy] != WhyOnly
	case KindRemote:
		return it.Meta[MetaWhy] == WhyStale
	case KindWorktree:
		return it.Meta[MetaWhy] == WhyMissing
	}
	return false
}

// Select returns what a plan takes: the item of every repository that is
// included, and what is chosen inside it.
func Select(items []recipe.Item, keep []string, marks map[string]bool) []recipe.Item {
	included := map[string]bool{}
	for _, it := range items {
		if it.Meta[MetaKind] == KindRepo && Chosen(it, keep, marks) {
			included[it.Group] = true
		}
	}
	var sel []recipe.Item
	for _, it := range items {
		if included[it.Group] && Chosen(it, keep, marks) {
			sel = append(sel, it)
		}
	}
	return sel
}
