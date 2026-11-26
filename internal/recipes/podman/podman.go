//go:build darwin

// Package podman cleans a Podman machine the right way.
//
// The machine's disk is a sparse file on the Mac. Deleting images frees
// blocks inside the Linux VM, but macOS only gets them back after fstrim
// runs in the VM, and that needs the machine running. So the order matters:
// remove, trim, then stop.
package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/romus/space/internal/humanize"
	"github.com/romus/space/pkg/recipe"
)

// ID is the recipe id.
const ID = "podman"

// Groups of items, one per section of the screen.
const (
	Images     = "images"
	Containers = "containers"
	Volumes    = "volumes"
	BuildCache = "cache"
)

// Toggle keys on the plan screen.
const (
	ToggleStop  = "stop"
	ToggleStart = "start"
)

func init() { recipe.Register(ID, func() recipe.Recipe { return &Recipe{} }) }

// Status is what the screen shows about the machine.
type Status struct {
	Version  string // "5.3"
	Machine  string // "podman-machine-default"
	VMType   string // "applehv"
	Running  bool
	CPUs     int
	Memory   int64 // bytes
	DiskSize int64 // provisioned size of the VM disk
	DiskPath string
	OnDisk   int64    // what the disk file really takes on the Mac
	Active   []string // running containers, stopped with the machine
}

// Recipe is the Podman recipe.
type Recipe struct {
	env    recipe.Env
	status Status
}

func (r *Recipe) ID() string { return ID }

func (r *Recipe) dataDir() string {
	return filepath.Join(r.env.Home, ".local", "share", "containers")
}

func (r *Recipe) Info() recipe.Info {
	return recipe.Info{
		Name:   "Podman",
		Path:   r.dataDir(),
		Source: "built-in",
		Hint:   "podman machine",
		Advice: "Don’t rm this — the VM disk is sparse. Press r for the recipe: remove images, fstrim, stop the machine.",
		About: "Removes the images, containers and volumes you pick, then runs fstrim inside the machine so macOS " +
			"learns which blocks are free, then stops the machine. Deleting the disk file by hand would destroy " +
			"every image and volume at once.",
	}
}

// Status returns what the last Inspect learned about the machine.
func (r *Recipe) Status() Status { return r.status }

func (r *Recipe) Detect(ctx context.Context, env recipe.Env) bool {
	r.env = env
	if _, err := env.Run.LookPath("podman"); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(r.dataDir(), "podman", "machine"))
	return err == nil
}

// num reads a JSON number that may be written as a string.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	*n = num(f)
	return nil
}

type machineJSON struct {
	Name     string
	Default  bool
	Running  bool
	VMType   string
	CPUs     num
	Memory   num
	DiskSize num
}

type imageJSON struct {
	ID         string `json:"Id"`
	Names      []string
	RepoTags   []string
	Size       num
	Created    num
	Containers num
	Dangling   bool
}

type containerJSON struct {
	ID      string `json:"Id"`
	Image   string
	ImageID string
	Names   []string
	State   string
	Created num
	Size    *struct {
		RootFsSize num `json:"rootFsSize"`
		RwSize     num `json:"rwSize"`
	}
}

type dfVerbose struct {
	Volumes []struct {
		VolumeName      string
		Links           num
		Size            num
		ReclaimableSize num
	}
}

type dfRow struct {
	Type           string
	RawSize        num
	RawReclaimable num
}

func (r *Recipe) json(ctx context.Context, v any, args ...string) error {
	out, err := r.env.Run.Output(ctx, "podman", args...)
	if err != nil {
		return fmt.Errorf("podman %s: %w", strings.Join(args, " "), err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("podman %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// bytesOr scales a value that may have been reported in a larger unit.
func bytesOr(n num, unit int64) int64 {
	v := int64(n)
	if v > 0 && v < 1<<20 {
		return v * unit
	}
	return v
}

func onDisk(path string) int64 {
	var st syscall.Stat_t
	if syscall.Lstat(path, &st) != nil {
		return 0
	}
	return st.Blocks * 512
}

func (r *Recipe) findDisk(name, vmType string) string {
	base := filepath.Join(r.dataDir(), "podman", "machine")
	patterns := []string{
		filepath.Join(base, vmType, name+"*.raw"),
		filepath.Join(base, "*", name+"*.raw"),
		filepath.Join(base, "*", "*.raw"),
		filepath.Join(base, "*", name+"*.qcow2"),
	}
	for _, p := range patterns {
		if m, _ := filepath.Glob(p); len(m) > 0 {
			sort.Strings(m)
			return m[0]
		}
	}
	return ""
}

func (r *Recipe) Inspect(ctx context.Context) ([]recipe.Item, error) {
	st := Status{}
	if out, err := r.env.Run.Output(ctx, "podman", "--version"); err == nil {
		f := strings.Fields(string(out))
		if len(f) > 0 {
			v := strings.Split(f[len(f)-1], ".")
			if len(v) >= 2 {
				st.Version = v[0] + "." + v[1]
			}
		}
	}

	var machines []machineJSON
	if err := r.json(ctx, &machines, "machine", "list", "--format", "json"); err != nil {
		return nil, err
	}
	if len(machines) == 0 {
		r.status = st
		return nil, nil
	}
	m := machines[0]
	for _, c := range machines {
		if c.Default {
			m = c
		}
	}
	for _, c := range machines {
		if c.Running {
			m = c
			break
		}
	}
	st.Machine, st.Running, st.VMType = m.Name, m.Running, m.VMType
	st.CPUs = int(m.CPUs)
	st.Memory = bytesOr(m.Memory, 1<<20)
	st.DiskSize = bytesOr(m.DiskSize, 1e9)
	st.DiskPath = r.findDisk(m.Name, m.VMType)
	st.OnDisk = onDisk(st.DiskPath)
	r.status = st
	if !st.Running {
		return nil, nil
	}

	var (
		images     []imageJSON
		containers []containerJSON
		verbose    dfVerbose
		df         []dfRow
	)
	if err := r.json(ctx, &images, "images", "--format", "json"); err != nil {
		return nil, err
	}
	if err := r.json(ctx, &containers, "ps", "--all", "--size", "--format", "json"); err != nil {
		return nil, err
	}
	// Volume sizes and the build cache are nice to have, not essential.
	_ = r.json(ctx, &verbose, "system", "df", "--verbose", "--format", "json")
	_ = r.json(ctx, &df, "system", "df", "--format", "json")

	now := r.env.Now()
	var items []recipe.Item

	running := map[string]string{} // image id → "api-dev · running"
	stopped := map[string][]string{}
	for _, c := range containers {
		name := short(c.ID)
		if len(c.Names) > 0 {
			name = c.Names[0]
		}
		isRunning := c.State == "running" || c.State == "paused"
		if isRunning {
			running[c.ImageID] = name + " · running"
			st.Active = append(st.Active, name)
		} else {
			stopped[c.ImageID] = append(stopped[c.ImageID], c.ID)
		}
		it := recipe.Item{
			ID:      c.ID,
			Group:   Containers,
			Title:   name,
			Kind:    recipe.Special,
			ModTime: time.Unix(int64(c.Created), 0),
			Default: !isRunning,
			Meta:    map[string]string{"id": short(c.ID), "image": c.Image, "state": c.State},
		}
		if c.Size != nil {
			it.Bytes = int64(c.Size.RwSize)
		}
		if isRunning {
			it.Locked = "running"
		}
		items = append(items, it)
	}
	r.status.Active = st.Active

	for _, im := range mergeImages(images) {
		title := "<none>:<none>"
		if len(im.Names) > 0 {
			title = im.Names[0]
		}
		it := recipe.Item{
			ID:      im.ID,
			Group:   Images,
			Title:   title,
			Bytes:   int64(im.Size),
			Kind:    recipe.Special,
			ModTime: time.Unix(int64(im.Created), 0),
			Meta:    map[string]string{"id": short(im.ID)},
		}
		if len(im.Names) > 1 {
			it.Meta["tags"] = strings.Join(im.Names, ",")
		}
		switch {
		case running[im.ID] != "":
			it.Locked = running[im.ID]
		case len(stopped[im.ID]) > 0:
			it.Meta["used"] = humanize.Plural(len(stopped[im.ID]), "stopped container")
			it.Meta["containers"] = strings.Join(stopped[im.ID], ",")
		default:
			it.Default = true
			if im.Dangling || title == "<none>:<none>" {
				it.Meta["used"] = "dangling"
			}
		}
		items = append(items, it)
	}

	for _, v := range verbose.Volumes {
		it := recipe.Item{
			ID:    v.VolumeName,
			Group: Volumes,
			Title: v.VolumeName,
			Bytes: int64(v.Size),
			Kind:  recipe.Special,
			Meta:  map[string]string{},
		}
		if v.Links > 0 {
			it.Locked = humanize.Plural(int(v.Links), "container")
		}
		items = append(items, it)
	}

	for _, row := range df {
		if strings.Contains(strings.ToLower(row.Type), "build") && row.RawSize > 0 {
			items = append(items, recipe.Item{
				ID:      "build-cache",
				Group:   BuildCache,
				Title:   "Build cache",
				Bytes:   int64(row.RawSize),
				Kind:    recipe.Cache,
				Default: true,
				Meta:    map[string]string{"used": "rebuilt on the next build"},
			})
		}
	}
	_ = now

	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if (a.Locked == "") != (b.Locked == "") {
			return a.Locked == ""
		}
		return a.Bytes > b.Bytes
	})
	return items, nil
}

// mergeImages folds the entries podman prints for one image into one.
// "podman images --format json" repeats an image once for each of its tags,
// so without this an image with two tags would be listed, counted and
// removed twice. Names holds every name the image goes by.
func mergeImages(list []imageJSON) []imageJSON {
	var out []imageJSON
	at := map[string]int{}
	for _, im := range list {
		i, seen := at[im.ID]
		if !seen {
			i = len(out)
			at[im.ID] = i
			names, tags := im.Names, im.RepoTags
			im.Names, im.RepoTags = nil, nil
			out = append(out, im)
			im.Names, im.RepoTags = names, tags
		}
		out[i].Names = addNames(out[i].Names, im.Names)
		out[i].RepoTags = addNames(out[i].RepoTags, im.RepoTags)
	}
	for i := range out {
		// RepoTags only stand in when podman gave no names at all: mixing
		// the two could make one tag look like two.
		if len(out[i].Names) == 0 {
			out[i].Names = out[i].RepoTags
		}
	}
	return out
}

// addNames appends the names that are not in the list yet.
func addNames(list, names []string) []string {
	for _, n := range names {
		if n != "" && !slices.Contains(list, n) {
			list = append(list, n)
		}
	}
	return list
}

func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (r *Recipe) Summary(items []recipe.Item) string {
	if r.status.Machine == "" {
		return "no machine"
	}
	if !r.status.Running {
		return "1 machine · stopped — start it to see what is inside"
	}
	var images, stopped int
	for _, it := range items {
		switch {
		case it.Group == Images:
			images++
		case it.Group == Containers && it.Locked == "":
			stopped++
		}
	}
	return fmt.Sprintf("1 machine · %s · %s", humanize.Plural(images, "image"),
		humanize.Plural(stopped, "stopped container"))
}

func (r *Recipe) Measure(ctx context.Context) (string, int64, bool) {
	if r.status.DiskPath == "" {
		return "", 0, false
	}
	return "machine disk on Mac", onDisk(r.status.DiskPath), true
}

func (r *Recipe) machineArgs(args ...string) []string {
	cmd := append([]string{"podman", "machine"}, args[0])
	if r.status.Machine != "" && r.status.Machine != "podman-machine-default" {
		cmd = append(cmd, r.status.Machine)
	}
	return append(cmd, args[1:]...)
}

// StartPlan starts the machine. Offered when it is stopped.
func (r *Recipe) StartPlan() recipe.Plan {
	return recipe.Plan{
		RecipeID: ID,
		Title:    "Podman",
		Steps: []recipe.Step{{
			Title:      "Start the machine",
			Cmd:        r.machineArgs("start"),
			Why:        "Images and containers can only be listed while the machine runs.",
			Reversible: true,
		}},
		Seconds: 20,
	}
}

func (r *Recipe) Plan(sel []recipe.Item, opt recipe.Options) (recipe.Plan, error) {
	st := r.status
	if st.Machine == "" {
		return recipe.Plan{}, fmt.Errorf("no Podman machine found")
	}
	if !st.Running {
		return r.StartPlan(), nil
	}

	var images, containers, volumes []recipe.Item
	var cache bool
	ctrSet := map[string]bool{}
	for _, it := range sel {
		if it.Locked != "" {
			return recipe.Plan{}, fmt.Errorf("%s is in use: %s", it.Title, it.Locked)
		}
		switch it.Group {
		case Images:
			images = append(images, it)
		case Containers:
			containers = append(containers, it)
			ctrSet[it.ID] = true
		case Volumes:
			volumes = append(volumes, it)
		case BuildCache:
			cache = true
		}
	}
	// A stopped container that uses a selected image has to go first.
	var extra []string
	for _, im := range images {
		for _, id := range strings.Split(im.Meta["containers"], ",") {
			if id != "" && !ctrSet[id] {
				ctrSet[id] = true
				extra = append(extra, short(id))
			}
		}
	}

	p := recipe.Plan{RecipeID: ID, Title: "Podman"}
	var freed int64

	if n := len(containers) + len(extra); n > 0 {
		cmd := []string{"podman", "rm"}
		for _, c := range containers {
			cmd = append(cmd, short(c.ID))
			freed += c.Bytes
		}
		cmd = append(cmd, extra...)
		why := "Stopped containers. Their writable layers are deleted."
		if len(extra) > 0 {
			why = "Stopped containers, including those that still hold a selected image."
		}
		p.Steps = append(p.Steps, recipe.Step{
			Title: "Remove " + humanize.Plural(n, "container"), Cmd: cmd, Why: why,
		})
	}
	// An image with several tags is removed by ID only when forced, so
	// those go in a step of their own and the rest are removed as usual.
	removed := 0
	if len(images) > 0 {
		plain := []string{"podman", "rmi"}
		forced := []string{"podman", "rmi", "--force"}
		seen := map[string]bool{}
		for _, im := range images {
			if seen[im.ID] {
				continue
			}
			seen[im.ID] = true
			freed += im.Bytes
			if im.Meta["tags"] != "" {
				forced = append(forced, short(im.ID))
			} else {
				plain = append(plain, short(im.ID))
			}
		}
		if n := len(plain) - 2; n > 0 {
			removed += n
			p.Steps = append(p.Steps, recipe.Step{
				Title: "Remove " + humanize.Plural(n, "image"), Cmd: plain,
				Why: "Images no container is using.",
			})
		}
		if n := len(forced) - 3; n > 0 {
			removed += n
			why := "Each has more than one tag."
			if n == 1 {
				why = "It has more than one tag."
			}
			p.Steps = append(p.Steps, recipe.Step{
				Title: "Remove " + humanize.Plural(n, "image") + " with several tags", Cmd: forced,
				Why: why + " Podman removes such an image by ID only with --force; all its tags go.",
			})
		}
		p.Steps = append(p.Steps, recipe.Step{
			Title: "Clean up dangling layers", Cmd: []string{"podman", "image", "prune", "-f"},
			Why: "Layers left without an image after the previous step.",
		})
	}
	if len(volumes) > 0 {
		cmd := []string{"podman", "volume", "rm"}
		for _, v := range volumes {
			cmd = append(cmd, v.ID)
			freed += v.Bytes
		}
		p.Steps = append(p.Steps, recipe.Step{
			Title: "Remove " + humanize.Plural(len(volumes), "volume"), Cmd: cmd,
			Why: "The data stored in these volumes is deleted.",
		})
	}
	if cache {
		for _, it := range sel {
			if it.Group == BuildCache {
				freed += it.Bytes
			}
		}
		p.Steps = append(p.Steps, recipe.Step{
			Title: "Remove the build cache", Cmd: []string{"podman", "builder", "prune", "-f"},
			Why: "Cached build layers. The next build starts from scratch.", Safe: true,
		})
	}

	p.Steps = append(p.Steps, recipe.Step{
		Title: "Tell your Mac which blocks are free",
		Cmd:   r.machineArgs("ssh", "--", "sudo", "fstrim", "-av"),
		Why:   "Only works while the machine runs — so it goes before stopping.",
		Safe:  true,
	})

	stop := opt.Toggle(ToggleStop, true)
	start := opt.Toggle(ToggleStart, false)
	p.Toggles = []recipe.Toggle{
		{Key: ToggleStop, Label: "stop when done", On: stop},
		{Key: ToggleStart, Label: "start again after", On: start},
	}
	if stop {
		why := "Can be turned off below."
		if len(st.Active) > 0 {
			why += " " + strings.Join(st.Active, " and ") + " will stop too."
		}
		p.Steps = append(p.Steps, recipe.Step{
			Title: "Stop the machine", Cmd: r.machineArgs("stop"), Why: why, Reversible: true,
		})
		if start {
			p.Steps = append(p.Steps, recipe.Step{
				Title: "Start the machine again", Cmd: r.machineArgs("start"),
				Why: "Brings the machine back up. Containers stay stopped.", Reversible: true,
			})
		}
	}
	if st.DiskPath != "" {
		p.Steps = append(p.Steps, recipe.Step{
			Title: "Check the result", Cmd: []string{"du", "-h", st.DiskPath},
			Why: "Compare size on disk before and after.", ReadOnly: true,
		})
	}

	p.Reclaim = freed
	p.Seconds = 15 + 5*len(p.Steps)
	p.Metric = &recipe.Metric{Label: "machine disk on Mac", Before: st.OnDisk, After: max(0, st.OnDisk-freed)}
	if removed > 0 {
		p.Summary = append(p.Summary, fmt.Sprintf("images removed\t%d", removed))
	}
	if stop && !start {
		if len(st.Active) > 0 {
			p.Summary = append(p.Summary, strings.Join(st.Active, ", ")+"\tstopped with the machine")
		}
		p.After = []recipe.Action{{Key: "s", Label: "start machine", Plan: r.StartPlan()}}
	}
	return p, nil
}
