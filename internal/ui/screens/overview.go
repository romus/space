//go:build darwin

package screens

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/romus/space/internal/classify"
	"github.com/romus/space/internal/engine"
	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/internal/recipes/agents"
	"github.com/romus/space/internal/recipes/git"
	"github.com/romus/space/internal/recipes/podman"
	"github.com/romus/space/internal/recipes/trash"
	"github.com/romus/space/internal/scan"
	"github.com/romus/space/pkg/recipe"
)

// Tabs of the overview.
const (
	TabFolders = iota
	TabFiles
	TabRecipes
)

// frow is one row of the Folders tab: a directory from the tree, or a file
// of the current directory.
type frow struct {
	name  string
	path  string
	size  int64
	files int64
	dir   *scan.Dir // nil for a file
	atime time.Time
	class classify.Class
}

func (r frow) openable() bool {
	return r.dir != nil && !r.dir.Bundle && !r.dir.Unreadable && !r.dir.Dataless
}

type recipeLoadedMsg struct {
	id    string
	items []recipe.Item
	err   error
}

var (
	sizeFilters = []int64{500e6, 1e9, 100e6, 10e6, 1e6}
	ageFilters  = []int{0, 30, 90, 365}
)

// Overview is the main screen: folders by size, top files and recipes.
type Overview struct {
	c      *Ctx
	sc     *scan.Scanner
	cancel context.CancelFunc
	cls    *classify.Classifier
	vol    scan.Volume
	prog   scan.Progress
	err    error

	recipes []*RecipeState
	tab     int

	cwd      *scan.Dir
	restore  string // folder to come back to once a rescan has reached it
	rows     []frow
	cur, off int
	listing  map[string][]scan.Entry
	nm       map[*scan.Dir]string
	marks    map[string]recipe.Item

	files                    []scan.File
	fcur, foff               int
	fsize, fage, ftype, fsrt int
	ffocus                   int // focused filter chip, or -1

	rcur       int
	showHidden bool
	info       bool
	help       bool

	search    string
	searching bool
	toast     toast
	ticks     int
	wasDone   bool
}

// NewOverview starts scanning and looks for recipes.
func NewOverview(c *Ctx, tab int) (*Overview, tea.Cmd) {
	o := &Overview{c: c, tab: tab, ffocus: -1, marks: map[string]recipe.Item{}}
	return o, o.start()
}

func (o *Overview) start() tea.Cmd {
	c := o.c
	if o.cancel != nil {
		o.cancel()
	}
	o.listing = map[string][]scan.Entry{}
	o.nm = map[*scan.Dir]string{}
	o.marks = map[string]recipe.Item{}
	o.wasDone = false
	o.cur, o.off, o.fcur, o.foff = 0, 0, 0, 0
	o.rows, o.files = nil, nil

	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel

	// Recipes first: the classifier needs to know which folders they own.
	o.recipes = nil
	var cmds []tea.Cmd
	var detected []recipe.Recipe
	for _, r := range recipe.All() {
		if !r.Detect(ctx, c.Env) {
			continue
		}
		detected = append(detected, r)
		o.recipes = append(o.recipes, &RecipeState{R: r})
		r := r
		cmds = append(cmds, func() tea.Msg {
			items, err := r.Inspect(ctx)
			return recipeLoadedMsg{id: r.ID(), items: items, err: err}
		})
	}
	o.cls = classify.New(c.Home(), detected)

	sc, err := scan.New(c.Root, scan.Options{Opaque: o.cls.Opaque, First: o.restore})
	o.sc, o.err = sc, err
	if err != nil {
		o.restore = ""
		return tea.Batch(cmds...)
	}
	o.cwd = sc.Root
	o.vol, _ = scan.VolumeOf(c.Root)
	sc.Start(ctx)
	return tea.Batch(cmds...)
}

// Rescan reads the disk again, after something was removed, and comes back
// to the folder that was open.
func (o *Overview) Rescan() tea.Cmd {
	o.restore = o.surviving(o.cwdPath())
	return o.start()
}

// surviving is path, or the nearest folder above it that still exists: the
// open folder itself may be what was removed. It is "" for the root, where
// a rescan starts anyway.
func (o *Overview) surviving(path string) string {
	root := filepath.Clean(o.c.Root)
	for p := filepath.Clean(path); p != root && len(p) > len(root); p = filepath.Dir(p) {
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// Close stops the scan.
func (o *Overview) Close() {
	if o.cancel != nil {
		o.cancel()
	}
}

func (o *Overview) Title() string { return o.c.Tilde(o.cwdPath()) }
func (o *Overview) Typing() bool  { return o.searching }

func (o *Overview) cwdPath() string {
	if o.restore != "" {
		return o.restore
	}
	if o.cwd == nil {
		return o.c.Root
	}
	return o.cwd.Path()
}

func (o *Overview) state(id string) *RecipeState {
	for _, rs := range o.recipes {
		if rs.R.ID() == id {
			return rs
		}
	}
	return nil
}

// forget drops a recipe that found nothing in this folder.
func (o *Overview) forget(id string) {
	out := o.recipes[:0:0]
	for _, rs := range o.recipes {
		if rs.R.ID() != id {
			out = append(out, rs)
		}
	}
	o.recipes = out
	o.rcur = min(o.rcur, max(0, len(o.visibleRecipes())-1))
}

// visibleRecipes are the recipes shown on the Recipes tab.
func (o *Overview) visibleRecipes() []*RecipeState {
	var out []*RecipeState
	for _, rs := range o.recipes {
		if o.showHidden || !o.c.State.IsHidden(rs.R.ID()) {
			out = append(out, rs)
		}
	}
	return out
}

func (o *Overview) hiddenCount() int {
	n := 0
	for _, rs := range o.recipes {
		if o.c.State.IsHidden(rs.R.ID()) {
			n++
		}
	}
	return n
}

// --- building rows ----------------------------------------------------------

func (o *Overview) rebuild() {
	if o.sc == nil {
		return
	}
	switch o.tab {
	case TabFolders:
		o.rebuildFolders()
	case TabFiles:
		o.rebuildFiles()
	}
}

func (o *Overview) match(name string) bool {
	return o.search == "" || strings.Contains(strings.ToLower(name), strings.ToLower(o.search))
}

func (o *Overview) rebuildFolders() {
	if o.restore != "" {
		// Not there yet: show nothing rather than the top folder.
		o.rows = nil
		return
	}
	keep := ""
	if o.cur < len(o.rows) {
		keep = o.rows[o.cur].path
	}
	base := o.cwd.Path()
	var rows []frow
	for _, d := range o.sc.Children(o.cwd) {
		if !o.match(d.Name) {
			continue
		}
		path := filepath.Join(base, d.Name)
		rows = append(rows, frow{
			name: d.Name, path: path, size: d.Size(), files: d.Files(), dir: d,
			atime: d.ModTime, class: o.cls.Path(path),
		})
	}
	entries, ok := o.listing[base]
	if !ok {
		entries, _ = scan.List(base)
		o.listing[base] = entries
	}
	for _, e := range entries {
		if !o.match(e.Name) {
			continue
		}
		path := filepath.Join(base, e.Name)
		rows = append(rows, frow{
			name: e.Name, path: path, size: e.Size, files: 1, atime: e.Atime, class: o.cls.Path(path),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].size != rows[j].size {
			return rows[i].size > rows[j].size
		}
		return rows[i].name < rows[j].name
	})
	o.rows = rows
	if keep != "" {
		for i, r := range rows {
			if r.path == keep {
				o.cur = i
				break
			}
		}
	}
	o.cur = min(max(o.cur, 0), max(0, len(rows)-1))
}

func (o *Overview) rebuildFiles() {
	keep := ""
	if o.fcur < len(o.files) {
		keep = o.files[o.fcur].Path()
	}
	now := o.c.Now()
	minSize := sizeFilters[o.fsize]
	minAge := ageFilters[o.fage]
	var out []scan.File
	for _, f := range o.sc.TopFiles() {
		if f.Size < minSize || !o.match(f.Name) {
			continue
		}
		if minAge > 0 && humanize.Days(f.Atime, now) < minAge {
			continue
		}
		if o.ftype != 0 && classify.TypeOf(f.Name) != classify.FileType(o.ftype) {
			continue
		}
		out = append(out, f)
	}
	switch o.fsrt {
	case 1: // least recently opened first
		sort.SliceStable(out, func(i, j int) bool { return out[i].Atime.Before(out[j].Atime) })
	case 2:
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	}
	o.files = out
	if keep != "" {
		for i, f := range out {
			if f.Path() == keep {
				o.fcur = i
				break
			}
		}
	}
	o.fcur = min(max(o.fcur, 0), max(0, len(out)-1))
}

// hint is the text in the last column of a folder row.
func (o *Overview) hint(r frow) string {
	if r.class.Hint != "" {
		if rs := o.state(r.class.RecipeID); rs != nil && rs.Loaded && rs.Err == nil {
			if n := sessions(rs.Items); n > 0 {
				return fmt.Sprintf("%s · %s", r.class.Hint, humanize.Plural(n, "session"))
			}
		}
		return r.class.Hint
	}
	if r.dir == nil {
		return ""
	}
	switch {
	case r.dir.Unreadable:
		return "not readable"
	case r.dir.Dataless:
		return "in iCloud, not downloaded"
	case r.dir.Bundle:
		return "bundle"
	}
	if !o.prog.Done {
		return ""
	}
	if h, ok := o.nm[r.dir]; ok {
		return h
	}
	h := ""
	if n, size := o.sc.NodeModules(r.dir); n > 0 && r.dir.Name != "node_modules" {
		h = fmt.Sprintf("node_modules × %d · %s", n, humanize.Bytes(size))
	} else if r.dir.OldFiles > 0 && filepath.Dir(r.path) == o.c.Home() {
		h = fmt.Sprintf("%s untouched for 1+ year", humanize.Plural(int(r.dir.OldFiles), "file"))
	}
	o.nm[r.dir] = h
	return h
}

func sessions(items []recipe.Item) int {
	n := 0
	for _, it := range items {
		if it.Kind == recipe.Special && it.Group != "" && !agents.IsExtra(it.Group) && it.Meta != nil {
			if _, isAgent := it.Meta["with"]; isAgent {
				n++
			}
		}
	}
	return n
}

// --- update -----------------------------------------------------------------

func (o *Overview) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case TickMsg:
		if o.sc == nil {
			return o, nil
		}
		o.ticks++
		o.prog = o.sc.Progress()
		if o.restore != "" {
			if d := o.sc.Lookup(o.restore); d != nil {
				o.cwd, o.restore = d, ""
				o.rebuild()
			} else if o.prog.Done {
				// The scan never entered it: stay at the top.
				o.restore = ""
				o.rebuild()
			}
		}
		switch {
		case o.prog.Done && !o.wasDone:
			o.wasDone = true
			o.vol, _ = scan.VolumeOf(o.c.Root)
			o.rebuild()
		case !o.prog.Done && o.ticks%3 == 0:
			o.rebuild()
		case len(o.rows) == 0 && o.tab == TabFolders && o.ticks%5 == 0:
			o.rebuild()
		}
	case recipeLoadedMsg:
		if errors.Is(msg.err, recipe.ErrNothingHere) {
			o.forget(msg.id) // it applies to this Mac, but not to this folder
			break
		}
		if rs := o.state(msg.id); rs != nil {
			rs.Items, rs.Err, rs.Loaded = msg.items, msg.err, true
		}
	case ToastMsg:
		o.toast.set(msg.Text)
	case tea.KeyPressMsg:
		return o.key(keyOf(msg), msg)
	}
	return o, nil
}

func (o *Overview) setTab(tab int) {
	o.tab = (tab%3 + 3) % 3
	o.search, o.searching, o.ffocus, o.info = "", false, -1, false
	o.rebuild()
}

func (o *Overview) key(k string, msg tea.KeyPressMsg) (Screen, tea.Cmd) {
	if o.help {
		o.help = false
		return o, nil
	}
	if o.searching {
		switch k {
		case "enter":
			o.searching = false
		case "esc":
			o.searching, o.search = false, ""
		case "backspace":
			if r := []rune(o.search); len(r) > 0 {
				o.search = string(r[:len(r)-1])
			}
		default:
			if msg.Text != "" {
				o.search += msg.Text
			}
		}
		o.rebuild()
		return o, nil
	}
	if o.ffocus >= 0 && o.tab == TabFiles {
		if o.filterKey(k) {
			return o, nil
		}
	}

	switch k {
	case "q":
		o.Close()
		return o, tea.Quit
	case "?":
		o.help = true
	case "tab":
		o.setTab(o.tab + 1)
	case "shift+tab":
		o.setTab(o.tab - 1)
	case "1", "2", "3":
		o.setTab(int(k[0] - '1'))
	case "/":
		if o.tab != TabRecipes {
			o.searching = true
		}
	case "esc":
		switch {
		case o.search != "":
			o.search = ""
			o.rebuild()
		case o.info:
			o.info = false
		case len(o.marks) > 0:
			o.marks = map[string]recipe.Item{}
			o.toast.set("Marks cleared")
		}
	case "u":
		o.marks = map[string]recipe.Item{}
	default:
		switch o.tab {
		case TabFolders:
			return o.foldersKey(k)
		case TabFiles:
			return o.filesKey(k)
		case TabRecipes:
			return o.recipesKey(k)
		}
	}
	return o, nil
}

func move(cur *int, k string, total, page int) bool {
	switch k {
	case "up", "k":
		*cur--
	case "down", "j":
		*cur++
	case "pgup":
		*cur -= page
	case "pgdown":
		*cur += page
	case "home", "g":
		*cur = 0
	case "end", "G":
		*cur = total - 1
	default:
		return false
	}
	*cur = min(max(*cur, 0), max(0, total-1))
	return true
}

func (o *Overview) foldersKey(k string) (Screen, tea.Cmd) {
	if move(&o.cur, k, len(o.rows), max(1, o.c.H-10)) {
		return o, nil
	}
	var row *frow
	if o.cur < len(o.rows) {
		row = &o.rows[o.cur]
	}
	switch k {
	case "right", "l":
		o.open(row)
	case "enter":
		if len(o.marks) > 0 {
			return o, o.plan(false)
		}
		o.open(row)
	case "left", "h", "backspace":
		if o.cwd != nil && o.cwd.Parent != nil {
			from := o.cwd.Path()
			o.cwd = o.cwd.Parent
			o.cur, o.off, o.search = 0, 0, ""
			o.rebuildFolders()
			for i, r := range o.rows {
				if r.path == from {
					o.cur = i
				}
			}
		}
	case "space":
		if row != nil {
			o.mark(recipe.Item{Path: row.path, Title: row.name, Bytes: row.size, Kind: row.class.Kind}, row.class)
			o.cur = min(o.cur+1, len(o.rows)-1)
		}
	case "d", "D":
		if row != nil && len(o.marks) == 0 {
			if !o.mark(recipe.Item{Path: row.path, Title: row.name, Bytes: row.size, Kind: row.class.Kind}, row.class) {
				return o, nil
			}
		}
		return o, o.plan(k == "D")
	case "r":
		if row != nil && row.class.RecipeID != "" {
			return o, o.openRecipe(row.class.RecipeID)
		}
		o.toast.set("No recipe for this row. Recipes are marked ◆")
	case "o":
		if row != nil {
			return o, reveal(row.path)
		}
	}
	return o, nil
}

func (o *Overview) open(row *frow) {
	if row == nil {
		return
	}
	if !row.openable() {
		if row.dir != nil && row.dir.Unreadable {
			o.toast.set("Not readable. Give your terminal Full Disk Access in System Settings › Privacy & Security")
		}
		return
	}
	o.cwd = row.dir
	o.cur, o.off, o.search = 0, 0, ""
	o.rebuildFolders()
}

// mark toggles a mark. It reports whether the item is marked afterwards.
func (o *Overview) mark(it recipe.Item, cl classify.Class) bool {
	if _, ok := o.marks[it.Path]; ok {
		delete(o.marks, it.Path)
		return false
	}
	switch {
	case cl.Kind == recipe.Special:
		o.toast.set("This belongs to a recipe — press r instead of removing it by hand")
		return false
	case cl.Protected:
		o.toast.set("Too important to remove whole — open it and pick what to remove inside")
		return false
	}
	sep := string(filepath.Separator)
	for p := range o.marks {
		if strings.HasPrefix(it.Path, p+sep) {
			o.toast.set("Its parent folder is already marked")
			return false
		}
		if strings.HasPrefix(p, it.Path+sep) {
			delete(o.marks, p)
		}
	}
	o.marks[it.Path] = it
	return true
}

func (o *Overview) marked() (n int, bytes int64) {
	for _, it := range o.marks {
		n++
		bytes += it.Bytes
	}
	return n, bytes
}

func (o *Overview) plan(permanent bool) tea.Cmd {
	if len(o.marks) == 0 {
		o.toast.set("Nothing is marked — press space on a row first")
		return nil
	}
	// Say so before the plan, not after it fails: started above the home
	// folder, space only looks.
	if _, err := engine.CheckRoot(o.c.Root, o.c.Home()); err != nil {
		o.toast.set("Here space only looks. To remove things, start it inside your home folder")
		return nil
	}
	items := make([]recipe.Item, 0, len(o.marks))
	for _, it := range o.marks {
		items = append(items, it)
	}
	p, err := trash.Plan(items, o.c.Root, permanent)
	if err != nil {
		o.toast.set(err.Error())
		return nil
	}
	engine.Prepare(&p)
	return push(NewPlan(o.c, PlanSpec{Plan: p, Crumbs: []string{o.c.Tilde(o.c.Root)}}))
}

func (o *Overview) filterKey(k string) bool {
	cycle := func(v *int, n, d int) { *v = ((*v+d)%n + n) % n }
	switch k {
	case "left", "h":
		cycle(&o.ffocus, 3, -1)
	case "right", "l":
		cycle(&o.ffocus, 3, 1)
	case "up", "k", "down", "j", "space":
		d := 1
		if k == "up" || k == "k" {
			d = -1
		}
		switch o.ffocus {
		case 0:
			cycle(&o.fsize, len(sizeFilters), d)
		case 1:
			cycle(&o.fage, len(ageFilters), d)
		case 2:
			cycle(&o.ftype, 4, d)
		}
		o.rebuildFiles()
	case "f", "enter", "esc":
		o.ffocus = -1
	default:
		return false
	}
	return true
}

func (o *Overview) filesKey(k string) (Screen, tea.Cmd) {
	if move(&o.fcur, k, len(o.files), max(1, (o.c.H-10)/2)) {
		return o, nil
	}
	var f *scan.File
	if o.fcur < len(o.files) {
		f = &o.files[o.fcur]
	}
	item := func() (recipe.Item, classify.Class) {
		cl := o.cls.Path(f.Path())
		return recipe.Item{Path: f.Path(), Title: f.Name, Bytes: f.Size, Kind: cl.Kind}, cl
	}
	switch k {
	case "space":
		if f != nil {
			it, cl := item()
			o.mark(it, cl)
			o.fcur = min(o.fcur+1, len(o.files)-1)
		}
	case "enter":
		return o, o.plan(false)
	case "d", "D":
		if f != nil && len(o.marks) == 0 {
			it, cl := item()
			if !o.mark(it, cl) {
				return o, nil
			}
		}
		return o, o.plan(k == "D")
	case "o":
		if f != nil {
			return o, reveal(f.Path())
		}
	case "f":
		o.ffocus = 0
	case "s":
		o.fsrt = (o.fsrt + 1) % 3
		o.rebuildFiles()
	case "r":
		if f != nil {
			if cl := o.cls.Path(f.Path()); cl.RecipeID != "" {
				return o, o.openRecipe(cl.RecipeID)
			}
		}
	}
	return o, nil
}

func (o *Overview) recipesKey(k string) (Screen, tea.Cmd) {
	list := o.visibleRecipes()
	if move(&o.rcur, k, len(list), 3) {
		o.info = false
		return o, nil
	}
	if o.rcur >= len(list) {
		if k == "X" || k == "shift+x" {
			o.showHidden = !o.showHidden
		}
		return o, nil
	}
	rs := list[o.rcur]
	switch k {
	case "right", "l", "enter", "r":
		return o, o.openRecipe(rs.R.ID())
	case "i":
		o.info = !o.info
	case "x":
		hidden := o.c.State.IsHidden(rs.R.ID())
		o.c.State.SetHidden(rs.R.ID(), !hidden)
		_ = o.c.State.Save()
		o.rcur = min(o.rcur, max(0, len(o.visibleRecipes())-1))
	case "X", "shift+x":
		o.showHidden = !o.showHidden
	}
	return o, nil
}

// openRecipe opens the screen of a recipe.
func (o *Overview) openRecipe(id string) tea.Cmd {
	rs := o.state(id)
	if rs == nil {
		return nil
	}
	if !rs.Loaded {
		o.toast.set("Still looking inside " + rs.R.Info().Name + "…")
		return nil
	}
	if rs.Err != nil {
		o.toast.set(rs.R.Info().Name + ": " + rs.Err.Error())
		return nil
	}
	switch id {
	case podman.ID:
		return push(NewPodman(o.c, rs))
	case git.ID:
		return push(NewGit(o.c, rs))
	case agents.ClaudeID, agents.CodexID:
		var list []*RecipeState
		for _, s := range o.recipes {
			if _, ok := s.R.(agents.Source); ok && s.Loaded && s.Err == nil {
				list = append(list, s)
			}
		}
		return push(NewAgents(o.c, list, id))
	}
	o.toast.set("No screen for this recipe yet")
	return nil
}
