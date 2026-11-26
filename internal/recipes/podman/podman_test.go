//go:build darwin

package podman

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/romus/space/pkg/recipe"
)

// fakeRunner answers podman commands from recorded output.
type fakeRunner struct {
	installed bool
	replies   map[string]string // command line → testdata file
	calls     []string
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.installed {
		return "/opt/homebrew/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (f *fakeRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if line == "podman --version" {
		return []byte("podman version 5.3.1\n"), nil
	}
	file, ok := f.replies[line]
	if !ok {
		return nil, errors.New("unexpected command: " + line)
	}
	return os.ReadFile(filepath.Join("testdata", file))
}

func running() map[string]string {
	return map[string]string{
		"podman machine list --format json":        "machine-list.json",
		"podman images --format json":              "images.json",
		"podman ps --all --size --format json":     "ps.json",
		"podman system df --verbose --format json": "df-verbose.json",
		"podman system df --format json":           "df.json",
	}
}

// setup makes a home directory with a machine folder and a disk image.
func setup(t *testing.T, replies map[string]string) (*Recipe, *fakeRunner, string) {
	t.Helper()
	home := t.TempDir()
	disk := filepath.Join(home, ".local/share/containers/podman/machine/applehv/podman-machine-default-arm64.raw")
	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{installed: true, replies: replies}
	r := &Recipe{}
	if !r.Detect(context.Background(), recipe.Env{Home: home, Run: run, Now: time.Now}) {
		t.Fatal("the recipe should detect the machine folder")
	}
	return r, run, disk
}

func byTitle(items []recipe.Item) map[string]recipe.Item {
	m := map[string]recipe.Item{}
	for _, it := range items {
		m[it.Group+":"+it.Title] = it
	}
	return m
}

func TestDetect(t *testing.T) {
	home := t.TempDir()
	env := recipe.Env{Home: home, Run: &fakeRunner{installed: true}, Now: time.Now}
	if (&Recipe{}).Detect(context.Background(), env) {
		t.Fatal("podman without a machine folder is not detected")
	}
	if err := os.MkdirAll(filepath.Join(home, ".local/share/containers/podman/machine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(&Recipe{}).Detect(context.Background(), env) {
		t.Fatal("podman with a machine folder is detected")
	}
	env.Run = &fakeRunner{installed: false}
	if (&Recipe{}).Detect(context.Background(), env) {
		t.Fatal("a machine folder without podman on PATH is not detected")
	}
}

func TestInspect(t *testing.T) {
	r, _, disk := setup(t, running())
	items, err := r.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	st := r.Status()
	if st.Machine != "podman-machine-default" || !st.Running || st.VMType != "applehv" || st.Version != "5.3" {
		t.Fatalf("status: %+v", st)
	}
	if st.CPUs != 4 || st.Memory != 4294967296 || st.DiskSize != 107374182400 {
		t.Fatalf("resources: %+v", st)
	}
	if st.DiskPath != disk || st.OnDisk <= 0 {
		t.Fatalf("disk: %s %d", st.DiskPath, st.OnDisk)
	}
	if strings.Join(st.Active, ",") != "api-dev,db-dev" {
		t.Fatalf("running containers: %v", st.Active)
	}

	m := byTitle(items)
	// In use by a running container: locked, named after the container.
	if got := m["images:localhost/api:dev"].Locked; got != "api-dev · running" {
		t.Fatalf("api:dev lock: %q", got)
	}
	if got := m["images:docker.io/library/postgres:16"].Locked; got != "db-dev · running" {
		t.Fatalf("postgres lock: %q", got)
	}
	// Unused: part of the default selection.
	for _, title := range []string{"localhost/indexer:build-2041", "<none>:<none>", "docker.io/library/node:20-bookworm"} {
		it := m["images:"+title]
		if !it.Default || it.Locked != "" {
			t.Fatalf("%s should be selected by default: %+v", title, it)
		}
	}
	if m["images:<none>:<none>"].Meta["used"] != "dangling" || m["images:<none>:<none>"].Meta["id"] != "9e04d7aa1f30" {
		t.Fatalf("dangling image: %+v", m["images:<none>:<none>"])
	}
	// Held by a stopped container: free to pick, but not picked for you.
	redis := m["images:docker.io/library/redis:7-alpine"]
	if redis.Default || redis.Locked != "" || redis.Meta["used"] != "1 stopped container" {
		t.Fatalf("redis: %+v", redis)
	}
	if !m["containers:old-cache"].Default || m["containers:api-dev"].Locked != "running" {
		t.Fatalf("containers: %+v / %+v", m["containers:old-cache"], m["containers:api-dev"])
	}
	// Volumes hold data: never selected by default, locked while in use.
	if m["volumes:pgdata"].Locked == "" || m["volumes:scratch"].Default || m["volumes:scratch"].Bytes != 300000000 {
		t.Fatalf("volumes: %+v / %+v", m["volumes:pgdata"], m["volumes:scratch"])
	}
	if got := r.Summary(items); got != "1 machine · 6 images · 1 stopped container" {
		t.Fatalf("summary: %q", got)
	}
}

func cmds(p recipe.Plan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, strings.Join(s.Cmd, " "))
	}
	return out
}

func TestPlanOrder(t *testing.T) {
	r, _, disk := setup(t, running())
	items, _ := r.Inspect(context.Background())
	var sel []recipe.Item
	for _, it := range recipe.DefaultSelection(items) {
		if it.Group == Images {
			sel = append(sel, it)
		}
	}
	plan, err := r.Plan(sel, recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Remove, prune, trim while the machine still runs, stop, then measure.
	want := []string{
		"podman rmi 3f9a1c07be52 9e04d7aa1f30 c81f2e4d9a07",
		"podman image prune -f",
		"podman machine ssh -- sudo fstrim -av",
		"podman machine stop",
		"du -h " + disk,
	}
	got := cmds(plan)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	tags := []string{"IRREVERSIBLE", "IRREVERSIBLE", "SAFE", "REVERSIBLE", "READ-ONLY"}
	for i, s := range plan.Steps {
		if s.Tag() != tags[i] {
			t.Fatalf("step %d (%s): tag %s, want %s", i+1, s.Title, s.Tag(), tags[i])
		}
	}
	if plan.Steps[0].Title != "Remove 3 images" || plan.Reclaim != 4800000000 {
		t.Fatalf("title %q, reclaim %d", plan.Steps[0].Title, plan.Reclaim)
	}
	if !strings.Contains(plan.Steps[3].Why, "api-dev and db-dev will stop too") {
		t.Fatalf("the stop step should name what stops: %q", plan.Steps[3].Why)
	}
	if len(plan.After) != 1 || plan.After[0].Label != "start machine" {
		t.Fatalf("a stopped machine should offer to start again: %+v", plan.After)
	}
}

func TestPlanToggles(t *testing.T) {
	r, _, _ := setup(t, running())
	r.Inspect(context.Background())

	keep, err := r.Plan(nil, recipe.Options{Toggles: map[string]bool{ToggleStop: false}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds(keep) {
		if strings.Contains(c, "machine stop") {
			t.Fatal("with the toggle off the machine keeps running")
		}
	}
	if !strings.Contains(strings.Join(cmds(keep), "\n"), "fstrim") {
		t.Fatal("trimming alone is still worth doing")
	}

	again, _ := r.Plan(nil, recipe.Options{Toggles: map[string]bool{ToggleStart: true}})
	got := cmds(again)
	stop, start := -1, -1
	for i, c := range got {
		switch {
		case strings.HasSuffix(c, "machine stop"):
			stop = i
		case strings.HasSuffix(c, "machine start"):
			start = i
		}
	}
	if stop < 0 || start != stop+1 {
		t.Fatalf("start again goes right after stop: %v", got)
	}
}

func TestPlanRemovesTheContainerHoldingAnImage(t *testing.T) {
	r, _, _ := setup(t, running())
	items, _ := r.Inspect(context.Background())
	redis := byTitle(items)["images:docker.io/library/redis:7-alpine"]
	plan, err := r.Plan([]recipe.Item{redis}, recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := cmds(plan)
	if got[0] != "podman rm ccc333ccc333" || got[1] != "podman rmi f2d9b3c61e04" {
		t.Fatalf("the stopped container must be removed before its image: %v", got)
	}
}

func TestPlanRefusesLockedItems(t *testing.T) {
	r, _, _ := setup(t, running())
	items, _ := r.Inspect(context.Background())
	api := byTitle(items)["images:localhost/api:dev"]
	if _, err := r.Plan([]recipe.Item{api}, recipe.Options{}); err == nil {
		t.Fatal("an image used by a running container must be refused")
	}
}

func TestStoppedMachine(t *testing.T) {
	replies := map[string]string{"podman machine list --format json": "machine-stopped.json"}
	r, run, _ := setup(t, replies)
	items, err := r.Inspect(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("a stopped machine has nothing to list: %v %v", items, err)
	}
	for _, c := range run.calls {
		if strings.Contains(c, "images") || strings.Contains(c, "ps ") {
			t.Fatalf("must not ask a stopped machine for images: %v", run.calls)
		}
	}
	if !strings.Contains(r.Summary(nil), "stopped") {
		t.Fatalf("summary: %q", r.Summary(nil))
	}
	plan, err := r.Plan(nil, recipe.Options{})
	if err != nil || len(plan.Steps) != 1 || strings.Join(plan.Steps[0].Cmd, " ") != "podman machine start" {
		t.Fatalf("the only thing to do is start it: %v %v", cmds(plan), err)
	}
}

// Podman lists an image once for each of its tags and refuses to remove it
// by ID without --force. It must be one row, counted once, in a forced step.
func TestImageWithSeveralTags(t *testing.T) {
	replies := running()
	replies["podman images --format json"] = "images-multitag.json"
	r, _, _ := setup(t, replies)
	items, err := r.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var images []recipe.Item
	for _, it := range items {
		if it.Group == Images {
			images = append(images, it)
		}
	}
	if len(images) != 2 {
		t.Fatalf("two images, one of them with two tags, are two rows: %+v", images)
	}
	node := byTitle(items)["images:docker.io/library/node:22-bookworm-slim"]
	if node.Bytes != 250000000 || !node.Default ||
		node.Meta["tags"] != "docker.io/library/node:22-bookworm-slim,docker.io/library/node:22-slim" {
		t.Fatalf("the image with two tags: %+v", node)
	}
	if got := byTitle(items)["images:docker.io/library/node:20-bookworm"].Meta["tags"]; got != "" {
		t.Fatalf("an image with one tag has no tag list: %q", got)
	}
	if got := r.Summary(items); got != "1 machine · 2 images · 1 stopped container" {
		t.Fatalf("summary: %q", got)
	}

	plan, err := r.Plan(images, recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := cmds(plan)
	want := []string{"podman rmi c81f2e4d9a07", "podman rmi --force 4e6c5a55f01d", "podman image prune -f"}
	if strings.Join(got[:3], "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if plan.Steps[0].Title != "Remove 1 image" || plan.Steps[1].Title != "Remove 1 image with several tags" {
		t.Fatalf("titles: %q, %q", plan.Steps[0].Title, plan.Steps[1].Title)
	}
	if !strings.Contains(plan.Steps[1].Why, "--force") || plan.Steps[1].Tag() != "IRREVERSIBLE" {
		t.Fatalf("the forced step should say why: %q (%s)", plan.Steps[1].Why, plan.Steps[1].Tag())
	}
	if plan.Reclaim != 1350000000 || plan.Summary[0] != "images removed\t2" {
		t.Fatalf("reclaim %d, summary %q", plan.Reclaim, plan.Summary)
	}

	// Only the image with several tags, even if it is handed over twice.
	plan, err = r.Plan([]recipe.Item{node, node}, recipe.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cmds(plan); got[0] != "podman rmi --force 4e6c5a55f01d" || got[1] != "podman image prune -f" {
		t.Fatalf("steps: %v", got)
	}
	if plan.Reclaim != 250000000 {
		t.Fatalf("the image is counted once: %d", plan.Reclaim)
	}
}

func TestMergeImages(t *testing.T) {
	// One entry per tag, each naming only its own tag.
	got := mergeImages([]imageJSON{
		{ID: "aaa", Names: []string{"a:1"}, Size: 10},
		{ID: "bbb", RepoTags: []string{"b:1"}},
		{ID: "aaa", Names: []string{"a:2"}, Size: 10},
		{ID: "ccc"},
	})
	if len(got) != 3 || strings.Join(got[0].Names, ",") != "a:1,a:2" || got[0].Size != 10 {
		t.Fatalf("merged: %+v", got)
	}
	// RepoTags stand in only when there are no names.
	if strings.Join(got[1].Names, ",") != "b:1" || len(got[2].Names) != 0 {
		t.Fatalf("names: %+v", got)
	}
	// The same tag under both keys is still one tag.
	one := mergeImages([]imageJSON{{ID: "ddd", Names: []string{"d:1"}, RepoTags: []string{"docker.io/library/d:1"}}})
	if len(one[0].Names) != 1 {
		t.Fatalf("one tag must not look like two: %+v", one[0])
	}
}
