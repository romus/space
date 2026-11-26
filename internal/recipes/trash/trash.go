// Package trash is the plain way of removing things: marked files, folders
// and caches go to the Trash, or are deleted outright when that is asked
// for. It is not shown on the Recipes tab; it is what "enter" on marked rows
// turns into.
package trash

import (
	"fmt"
	"sort"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// ID is the plan's recipe id.
const ID = "trash"

// Plan builds the steps for a set of marked items. root is the folder being
// scanned: nothing outside it is ever removed.
//
// Everything goes to the Trash, caches included, so the default is always
// reversible. With permanent the items are deleted instead: plain ones as an
// irreversible step, caches as a safe one, since they are rebuilt.
func Plan(items []recipe.Item, root string, permanent bool) (recipe.Plan, error) {
	if len(items) == 0 {
		return recipe.Plan{}, fmt.Errorf("nothing is marked")
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Bytes > items[j].Bytes })

	var plain, caches []recipe.Item
	for _, it := range items {
		switch it.Kind {
		case recipe.Cache:
			caches = append(caches, it)
		case recipe.Plain:
			plain = append(plain, it)
		default:
			return recipe.Plan{}, fmt.Errorf("%s cannot be removed this way", it.Path)
		}
	}

	p := recipe.Plan{RecipeID: ID, Title: "Remove"}
	p.Seconds = 1 + len(items)/50
	if !permanent {
		// items is already sorted by size and holds only plain and cache.
		r := &recipe.Remove{Paths: paths(items), ToTrash: true, Root: root, Bytes: recipe.Sum(items)}
		p.Steps = append(p.Steps, recipe.Step{
			Title:      fmt.Sprintf("Move %s to the Trash", noun(len(items))),
			Why:        fmt.Sprintf("%s. Space comes back when you empty the Trash.", humanize.Bytes(r.Bytes)),
			Remove:     r,
			Reversible: true,
		})
		p.ToTrash = r.Bytes
		return p, nil
	}
	if len(plain) > 0 {
		r := &recipe.Remove{Paths: paths(plain), Root: root, Bytes: recipe.Sum(plain)}
		p.Steps = append(p.Steps, recipe.Step{
			Title:  fmt.Sprintf("Delete %s forever", noun(len(plain))),
			Why:    fmt.Sprintf("%s. This cannot be undone.", humanize.Bytes(r.Bytes)),
			Remove: r,
		})
		p.Reclaim += r.Bytes
	}
	if len(caches) > 0 {
		r := &recipe.Remove{Paths: paths(caches), Root: root, Bytes: recipe.Sum(caches)}
		p.Steps = append(p.Steps, recipe.Step{
			Title:  fmt.Sprintf("Delete %s", cacheNoun(len(caches))),
			Why:    fmt.Sprintf("%s. Caches are rebuilt the next time they are needed.", humanize.Bytes(r.Bytes)),
			Remove: r,
			Safe:   true,
		})
		p.Reclaim += r.Bytes
	}
	return p, nil
}

// EmptyTrash is the follow-up offered after things were moved to the Trash.
func EmptyTrash() recipe.Plan {
	return recipe.Plan{
		RecipeID: ID,
		Title:    "Empty Trash",
		Steps: []recipe.Step{{
			Title: "Empty the Trash",
			Cmd:   []string{"osascript", "-e", `tell application "Finder" to empty trash`},
			Why:   "Everything in the Trash is deleted for good. macOS may ask to let your terminal control Finder.",
		}},
		Seconds: 5,
	}
}

func paths(items []recipe.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Path
	}
	return out
}

func noun(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

func cacheNoun(n int) string {
	if n == 1 {
		return "1 cache"
	}
	return fmt.Sprintf("%d caches", n)
}
