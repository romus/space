//go:build darwin

package screens

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/romus/space/internal/classify"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/ui/kit"
	"github.com/romus/space/pkg/recipe"
)

// kindColor is the colour of a row's glyph and hint.
func kindColor(c *Ctx, k recipe.Kind) color.Color {
	t := c.Theme
	switch k {
	case recipe.Cache:
		return t.Safe
	case recipe.Special:
		return t.Recipe
	}
	return t.Text
}

// glyphColor is the colour of the mark in front of a row.
func glyphColor(c *Ctx, cl classify.Class) color.Color {
	if cl.Protected {
		return c.Theme.Muted
	}
	return kindColor(c, cl.Kind)
}

func (o *Overview) Lines() []string {
	c, t := o.c, o.c.Theme
	w := c.W
	if o.err != nil {
		top := []string{
			header(c, nil, nil), kit.Blank(t, w),
			kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Danger), "Cannot scan: "+o.err.Error())}, nil),
		}
		return kit.Frame(t, w, c.H, top, []string{kit.Footer(t, w, []kit.Hint{{"q", "quit"}}, nil, "", "")})
	}

	// A long path keeps its end: that is the folder you are looking at.
	gauge := diskGauge(c, o.vol)
	room := w - len("space  ") - 2
	for _, s := range gauge {
		room -= kit.Width(s.Text)
	}
	path := strings.TrimSuffix(c.Tilde(o.cwdPath()), "/") + "/"
	if kit.Width(path) > room {
		path = strings.TrimRight(kit.FitLeft(path, max(8, room)), " ")
	}
	crumb := []kit.Span{kit.Sp(t.S(t.Muted), path)}
	top := []string{header(c, crumb, gauge), kit.Blank(t, w)}

	badge := ""
	if n := len(o.visibleRecipes()); n > 0 {
		badge = fmt.Sprint(n)
	}
	tabs := []kit.Tab{{Label: "Folders"}, {Label: "Files"}, {Label: "Recipes", Badge: badge, Mark: true}}
	line, rule := kit.Tabs(t, w, tabs, o.tab, o.tabStatus())
	top = append(top, line, rule)

	if o.help {
		return kit.Frame(t, w, c.H, append(top, o.helpLines()...),
			[]string{kit.Footer(t, w, []kit.Hint{{"any key", "close"}}, nil, "", "")})
	}

	var body, bottom []string
	switch o.tab {
	case TabFolders:
		body, bottom = o.foldersView()
	case TabFiles:
		body, bottom = o.filesView()
	default:
		body, bottom = o.recipesView()
	}
	return kit.Frame(t, w, c.H, append(top, body...), bottom)
}

// tabStatus is the right side of the tab bar.
func (o *Overview) tabStatus() []kit.Span {
	c, t := o.c, o.c.Theme
	muted := t.S(t.Muted)
	if o.searching || o.search != "" {
		cursor := ""
		if o.searching {
			cursor = "▍"
		}
		return []kit.Span{kit.Sp(muted, "/"), kit.Sp(t.S(t.Strong), o.search+cursor)}
	}
	switch o.tab {
	case TabFiles:
		labels := []string{
			"≥ " + humanize.Bytes(sizeFilters[o.fsize]),
			"any age",
			classify.FileType(o.ftype).String(),
		}
		if d := ageFilters[o.fage]; d > 0 {
			labels[1] = fmt.Sprintf("%d+ days", d)
			if d >= 365 {
				labels[1] = "1+ year"
			}
		}
		var out []kit.Span
		for i, l := range labels {
			if i > 0 {
				out = append(out, kit.Sp(t.Base(), " "))
			}
			out = append(out, kit.Chip(t, l, o.ffocus == i)...)
		}
		return out
	case TabRecipes:
		var total int64
		for _, rs := range o.visibleRecipes() {
			total += rs.Reclaim()
		}
		if total == 0 {
			return nil
		}
		return []kit.Span{
			kit.Sp(muted, "up to "), kit.Sp(t.S(t.Strong), "≈ "+humanize.Bytes(total)), kit.Sp(muted, " reclaimable"),
		}
	}
	p := o.prog
	if !p.Done {
		return []kit.Span{
			kit.Sp(t.S(t.Strong), kit.Spinner(c.Frame)),
			kit.Sp(muted, fmt.Sprintf(" scanning · %s files · %.1f s", humanize.Short(p.Files), p.Elapsed.Seconds())),
		}
	}
	text := fmt.Sprintf("%s files · %.1f s · by size on disk ↓", humanize.Short(p.Files), p.Elapsed.Seconds())
	if p.Unreadable > 0 {
		text = humanize.Plural(int(p.Unreadable), "folder") + " not readable · " + text
	}
	return []kit.Span{kit.Sp(muted, text)}
}

func (o *Overview) toastLine() (string, bool) {
	t := o.c.Theme
	if msg := o.toast.get(); msg != "" {
		return kit.Row(o.c.W, t.Base(), []kit.Span{kit.Sp(t.S(t.Strong), msg)}, nil), true
	}
	return "", false
}

func (o *Overview) markSummary(suffix string) ([]kit.Span, string, string) {
	t := o.c.Theme
	n, bytes := o.marked()
	if n == 0 {
		return nil, "", ""
	}
	return []kit.Span{
		kit.Sp(t.S(t.Muted), fmt.Sprintf("%d marked · ", n)),
		kit.Sp(t.S(t.Strong), humanize.Bytes(bytes)),
		kit.Sp(t.S(t.Muted), suffix),
	}, "⏎", "plan"
}

// --- folders ----------------------------------------------------------------

func (o *Overview) foldersView() (body, bottom []string) {
	c, t := o.c, o.c.Theme
	w := c.W

	const barW = 30
	nameW := 26
	hintW := w - (2 + 2 + nameW + barW + 2 + 11 + 7) - 3
	if hintW < 14 {
		hintW = 0
		nameW = min(48, max(12, w-(2+2+barW+2+11+7)))
	}

	visible := max(1, c.H-9)
	o.off = kit.Window(o.cur, o.off, len(o.rows), visible)

	var maxSize int64
	for _, r := range o.rows {
		maxSize = max(maxSize, r.size)
	}
	total := o.cwd.Size()

	for i := o.off; i < len(o.rows) && i < o.off+visible; i++ {
		r := o.rows[i]
		cur := i == o.cur
		st := func(fg color.Color) lipgloss.Style {
			if cur {
				return t.Sel(fg)
			}
			return t.S(fg)
		}
		name, bar := t.Text, t.Bar
		if cur {
			name, bar = t.Strong, t.BarCur
		}
		kc := kindColor(c, r.class.Kind)
		mark := ""
		if _, ok := o.marks[r.path]; ok {
			mark = "✓"
		}
		label := r.name
		if r.dir != nil && !r.dir.Bundle {
			label += "/"
		}
		frac, pct := 0.0, ""
		if maxSize > 0 {
			frac = float64(r.size) / float64(maxSize)
		}
		if total > 0 {
			pct = fmt.Sprintf("%.0f%%", float64(r.size)/float64(total)*100)
		}
		spans := []kit.Span{
			kit.Cell(st(t.Strong), mark, 2),
			kit.Cell(st(glyphColor(c, r.class)), r.class.Glyph(), 2),
			kit.Cell(st(name), label, nameW),
			kit.Cell(st(bar), kit.Bar(frac, barW), barW+2),
			kit.CellR(st(name), humanize.Bytes(r.size), 11),
			kit.CellR(st(t.Muted), pct, 7),
		}
		if hintW > 0 {
			spans = append(spans, kit.Cell(st(t.Text), "", 3), kit.Cell(st(kc), o.hint(r), hintW))
		}
		body = append(body, kit.Row(w, st(t.Text), spans, nil))
	}
	if len(o.rows) == 0 {
		msg := "Reading…"
		switch {
		case o.search != "":
			msg = "Nothing matches “" + o.search + "”"
		case o.prog.Done:
			msg = "This folder is empty"
		}
		body = append(body, kit.Blank(t, w), kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Muted), "  "+msg)}, nil))
	}

	// Detail of the current row.
	bottom = append(bottom, kit.Rule(t, w))
	if o.cur < len(o.rows) {
		r := o.rows[o.cur]
		glyph := r.class.Glyph()
		if glyph == "" {
			glyph = "·"
		}
		what := r.class.Label()
		advice := r.class.Advice()
		if rs := o.state(r.class.RecipeID); rs != nil {
			info := rs.R.Info()
			what, advice = info.Name, info.Advice
		}
		desc := fmt.Sprintf("%s · %s on disk", what, humanize.Bytes(r.size))
		if r.dir != nil && r.files > 0 {
			desc += " · " + humanize.Plural(int(r.files), "file")
		}
		second := kit.Sp(t.S(t.Muted), "   "+advice)
		if msg := o.toast.get(); msg != "" {
			// A note takes the place of the advice; the path stays put.
			second = kit.Sp(t.S(t.Strong), "   "+msg)
		}
		bottom = append(bottom,
			kit.Row(w, t.Base(), []kit.Span{
				kit.Cell(t.S(glyphColor(c, r.class)), glyph, 3),
				kit.Sp(t.S(t.Strong), c.Tilde(r.path)),
				kit.Sp(t.S(t.Muted), "  "+desc),
			}, nil),
			kit.Row(w, t.Base(), []kit.Span{second}, nil))
	} else if line, ok := o.toastLine(); ok {
		bottom = append(bottom, line, kit.Blank(t, w))
	} else {
		bottom = append(bottom, kit.Blank(t, w), kit.Blank(t, w))
	}

	hints := []kit.Hint{{"↑↓", "move"}, {"→", "open"}, {"␣", "mark"}}
	if len(o.recipes) > 0 {
		hints = append(hints, kit.Hint{Key: "r", Label: "recipe"})
	}
	hints = append(hints, kit.Hint{Key: "tab", Label: "view"}, kit.Hint{Key: "/", Label: "search"}, kit.Hint{Key: "?", Label: "more"})
	sum, key, action := o.markSummary("")
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w, hints, sum, key, action))
	return body, bottom
}

// --- files ------------------------------------------------------------------

func (o *Overview) filesView() (body, bottom []string) {
	c, t := o.c, o.c.Theme
	w := c.W

	viaW := 24
	if w < 100 {
		viaW = 0
	}
	fileW := max(16, w-(2+2+11+15)-viaW-3)

	faint := t.S(t.Faint)
	head := []kit.Span{
		kit.Cell(faint, "", 4), kit.Cell(faint, "FILE", fileW),
		kit.CellR(faint, "ON DISK", 11), kit.CellR(faint, "LAST OPENED", 15),
	}
	if viaW > 0 {
		head = append(head, kit.Cell(faint, "", 3), kit.Cell(faint, "REMOVED VIA", viaW))
	}
	body = append(body, kit.Row(w, t.Base(), head, nil))

	visible := max(1, (c.H-9)/2)
	o.foff = kit.Window(o.fcur, o.foff, len(o.files), visible)
	now := c.Now()

	for i := o.foff; i < len(o.files) && i < o.foff+visible; i++ {
		f := o.files[i]
		path := f.Path()
		cl := o.cls.Path(path)
		cur := i == o.fcur
		st := func(fg color.Color) lipgloss.Style {
			if cur {
				return t.Sel(fg)
			}
			return t.S(fg)
		}
		name := t.Text
		if cur {
			name = t.Strong
		}
		kc := kindColor(c, cl.Kind)
		mark := ""
		if _, ok := o.marks[path]; ok {
			mark = "✓"
		}
		label := f.Name
		if f.Bundle && f.Count > 0 {
			label = fmt.Sprintf("%s · %s", f.Name, humanize.Plural(int(f.Count), "file"))
		}
		first := []kit.Span{
			kit.Cell(st(t.Strong), mark, 2),
			kit.Cell(st(glyphColor(c, cl)), cl.Glyph(), 2),
			kit.Cell(st(name), label, fileW),
			kit.CellR(st(name), humanize.Bytes(f.Size), 11),
			kit.CellR(st(t.Muted), humanize.Ago(f.Atime, now), 15),
		}
		if viaW > 0 {
			first = append(first, kit.Cell(st(t.Text), "", 3), kit.Cell(st(kc), cl.How, viaW))
		}
		second := []kit.Span{
			kit.Cell(st(t.Faint), "", 4),
			kit.Sp(st(t.Faint), kit.FitLeft(c.Tilde(filepath.Dir(path)), fileW)),
		}
		body = append(body, kit.Row(w, st(t.Text), first, nil), kit.Row(w, st(t.Text), second, nil))
	}
	if len(o.files) == 0 {
		msg := "No files this large yet — still scanning"
		if o.prog.Done {
			msg = "No files match these filters. Press f to change them."
		}
		body = append(body, kit.Blank(t, w), kit.Row(w, t.Base(), []kit.Span{kit.Sp(t.S(t.Muted), "  "+msg)}, nil))
	}

	bottom = append(bottom, kit.Rule(t, w))
	if line, ok := o.toastLine(); ok {
		bottom = append(bottom, line)
	} else if o.fcur < len(o.files) {
		f := o.files[o.fcur]
		cl := o.cls.Path(f.Path())
		muted := t.S(t.Muted)
		switch {
		case !f.Bundle && f.Length > f.Size+f.Size/5 && f.Length-f.Size > 100e6:
			bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{
				kit.Cell(t.S(kindColor(c, cl.Kind)), "◆", 3),
				kit.Sp(muted, fmt.Sprintf("File size %s, %s on disk — it’s a sparse file. We always count space ",
					humanize.Bytes(f.Length), humanize.Bytes(f.Size))),
				kit.Sp(t.S(t.Text), "on disk"), kit.Sp(muted, "."),
			}, nil))
		default:
			glyph := cl.Glyph()
			if glyph == "" {
				glyph = "·"
			}
			advice := cl.Advice()
			if rs := o.state(cl.RecipeID); rs != nil {
				advice = rs.R.Info().Advice
			}
			bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{
				kit.Cell(t.S(glyphColor(c, cl)), glyph, 3), kit.Sp(muted, advice),
			}, nil))
		}
	} else {
		bottom = append(bottom, kit.Blank(t, w))
	}

	sorts := []string{"size", "least recently opened", "name"}
	hints := []kit.Hint{{"␣", "mark"}, {"o", "reveal in Finder"}, {"f", "filters"}, {"s", "sort: " + sorts[o.fsrt]}}
	if o.ffocus >= 0 {
		hints = []kit.Hint{{"←→", "filter"}, {"↑↓", "change"}, {"esc", "done"}}
	}
	sum, key, action := o.markSummary(" to Trash")
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w, hints, sum, key, action))
	return body, bottom
}

// --- recipes ----------------------------------------------------------------

func (o *Overview) recipesView() (body, bottom []string) {
	c, t := o.c, o.c.Theme
	w := c.W
	list := o.visibleRecipes()
	o.rcur = min(o.rcur, max(0, len(list)-1))

	nameW := 30
	foundW := max(10, w-(2+nameW+12+12))
	faint := t.S(t.Faint)
	body = append(body, kit.Row(w, t.Base(), []kit.Span{
		kit.Cell(faint, "", 2), kit.Cell(faint, "RECIPE", nameW), kit.Cell(faint, "FOUND", foundW),
		kit.CellR(faint, "RECLAIM ≈", 12), kit.CellR(faint, "SOURCE", 12),
	}, nil))

	for i, rs := range list {
		info := rs.R.Info()
		cur := i == o.rcur
		st := func(fg color.Color) lipgloss.Style {
			if cur {
				return t.Sel(fg)
			}
			return t.S(fg)
		}
		name := t.Text
		if cur {
			name = t.Strong
		}
		found, gain := "looking…", ""
		switch {
		case rs.Err != nil:
			found = rs.Err.Error()
		case rs.Loaded:
			found = rs.R.Summary(rs.Items)
			if n := rs.Reclaim(); n > 0 {
				gain = humanize.Bytes(n)
			} else {
				gain = "—"
			}
		}
		source := info.Source
		if c.State.IsHidden(rs.R.ID()) {
			source = "hidden"
		}
		body = append(body,
			kit.Row(w, st(t.Text), []kit.Span{
				kit.Cell(st(t.Recipe), "◆", 2), kit.Cell(st(name), info.Name, nameW),
				kit.Cell(st(t.Found), found, foundW),
				kit.CellR(st(name), gain, 12), kit.CellR(st(t.Faint), source, 12),
			}, nil),
			kit.Row(w, st(t.Text), []kit.Span{
				kit.Cell(st(t.Faint), "", 2), kit.Cell(st(t.Faint), c.Tilde(info.Path), nameW),
			}, nil),
			kit.Blank(t, w))
	}
	if len(list) == 0 {
		body = append(body, kit.Blank(t, w), kit.Row(w, t.Base(), []kit.Span{
			kit.Sp(t.S(t.Muted), "  Nothing here needs a recipe: no Podman machine, no Git repositories, no Claude Code or Codex sessions."),
		}, nil))
	}

	bottom = append(bottom, kit.Rule(t, w))
	muted := t.S(t.Muted)
	switch {
	case o.toast.get() != "":
		line, _ := o.toastLine()
		bottom = append(bottom, line, kit.Blank(t, w))
	case o.info && o.rcur < len(list):
		lines := wrap(list[o.rcur].R.Info().About, w-3)
		for i, l := range lines {
			glyph := ""
			if i == 0 {
				glyph = "◆"
			}
			bottom = append(bottom, kit.Row(w, t.Base(), []kit.Span{kit.Cell(t.S(t.Recipe), glyph, 3), kit.Sp(muted, l)}, nil))
		}
	default:
		bottom = append(bottom,
			kit.Row(w, t.Base(), []kit.Span{
				kit.Cell(t.S(t.Recipe), "◆", 3), kit.Sp(muted, "A "), kit.Sp(t.S(t.Strong), "recipe"),
				kit.Sp(muted, " handles what you can’t just delete. It knows the right order of commands and shows it before running."),
			}, nil),
			kit.Row(w, t.Base(), []kit.Span{
				kit.Sp(muted, "   Add your own: "), kit.Sp(t.S(t.Text), c.Tilde(c.Paths.RecipeDir())+"/*.toml"),
				kit.Sp(muted, " or a "), kit.Sp(t.S(t.Text), "space-recipe-*"), kit.Sp(muted, " binary on PATH (coming later)."),
			}, nil))
	}

	hints := []kit.Hint{{"→", "open"}, {"i", "what it does"}, {"x", "hide"}}
	if n := o.hiddenCount(); n > 0 {
		label := fmt.Sprintf("show %d hidden", n)
		if o.showHidden {
			label = "hide them again"
		}
		hints = append(hints, kit.Hint{Key: "X", Label: label})
	}
	key, action := "", ""
	if o.rcur < len(list) {
		key, action = "→", list[o.rcur].R.Info().Name
	}
	bottom = append(bottom, kit.Blank(t, w), kit.Footer(t, w, hints, nil, key, action))
	return body, bottom
}

// --- help -------------------------------------------------------------------

func (o *Overview) helpLines() []string {
	c, t := o.c, o.c.Theme
	type group struct {
		title string
		keys  [][2]string
	}
	groups := []group{
		{"Moving", [][2]string{
			{"↑ ↓  j k", "move"}, {"→  l", "open folder"}, {"←  h", "parent folder"},
			{"pgup pgdn  g G", "jump"}, {"tab  1 2 3", "Folders · Files · Recipes"}, {"/", "search, esc clears"},
		}},
		{"Removing", [][2]string{
			{"space", "mark a row"}, {"u  esc", "clear marks"}, {"enter", "plan for marked rows"},
			{"d", "plan: move to the Trash"}, {"D", "plan: delete forever"}, {"r", "open the recipe of a ◆ row"},
		}},
		{"Other", [][2]string{
			{"o", "reveal in Finder"}, {"f  s", "filters and sort on Files"},
			{"t", "theme: system → light → dark (now " + string(c.Mode) + ")"}, {"q", "quit"},
		}},
		{"Marks", [][2]string{
			{"·", "plain files — go to the Trash"}, {"●", "cache — safe, will be rebuilt"},
			{"◆", "recipe — needs the right order of commands"}, {"○", "protected — open it and pick what to remove inside"},
		}},
	}
	lines := []string{kit.Blank(t, c.W)}
	for _, g := range groups {
		lines = append(lines, kit.Row(c.W, t.Base(), []kit.Span{kit.Sp(t.S(t.Faint), "  "+strings.ToUpper(g.title))}, nil))
		for _, k := range g.keys {
			lines = append(lines, kit.Row(c.W, t.Base(), []kit.Span{
				kit.Cell(t.Base(), "", 4), kit.Cell(t.S(t.Text), k[0], 18), kit.Sp(t.S(t.Muted), k[1]),
			}, nil))
		}
		lines = append(lines, kit.Blank(t, c.W))
	}
	lines = append(lines, kit.Row(c.W, t.Base(), []kit.Span{
		kit.Sp(t.S(t.Muted), "  Nothing runs without a plan. The plan is exactly what will run; c copies it as a shell script."),
	}, nil))
	return lines
}
