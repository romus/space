//go:build darwin

// Package scan walks a directory tree in parallel and adds up how much space
// everything takes on disk.
//
// Size is always st_blocks × 512: what the file really occupies, not its
// length. Hard links are counted once, other volumes are not entered, and
// iCloud placeholders are never downloaded.
package scan

import (
	"container/heap"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	sfDataless   = 0x40000000 // SF_DATALESS: content lives in iCloud
	minTopFile   = 1 << 20    // files smaller than this never reach the top list
	topCapacity  = 5000       // how many of the largest files are kept
	staleAfter   = 365 * 24 * time.Hour
	blockSize    = 512
	nodeModules  = "node_modules"
	maxSyncDepth = 512
)

// Dir is a directory in the tree. Only directories are kept in memory; the
// files of a directory are listed on demand with List.
type Dir struct {
	Name   string // base name; the root holds its full path
	Parent *Dir

	dirs  []*Dir       // guarded by Scanner.mu
	size  atomic.Int64 // on-disk bytes of the whole subtree
	files atomic.Int64 // number of files in the whole subtree

	OldFiles   int32     // own files not opened for a year or more
	ModTime    time.Time // of the directory itself
	Unreadable bool      // could not be opened (no permission)
	Dataless   bool      // iCloud placeholder, not entered
	Bundle     bool      // opaque package: shown as a single item
}

// Size is the on-disk size of the subtree so far.
func (d *Dir) Size() int64 { return d.size.Load() }

// Files is the number of files in the subtree so far.
func (d *Dir) Files() int64 { return d.files.Load() }

// Path is the absolute path of the directory.
func (d *Dir) Path() string {
	if d.Parent == nil {
		return d.Name
	}
	return filepath.Join(d.Parent.Path(), d.Name)
}

// File is one of the largest files, or an opaque bundle shown as a file.
type File struct {
	Dir    *Dir
	Name   string
	Size   int64 // on disk
	Length int64 // apparent size; larger than Size for sparse files
	Atime  time.Time
	Bundle bool
	Count  int64 // files inside a bundle
}

// Path is the absolute path of the file.
func (f File) Path() string { return filepath.Join(f.Dir.Path(), f.Name) }

// Progress is a snapshot of how far the scan has got.
type Progress struct {
	Files      int64
	Dirs       int64
	Unreadable int64
	Elapsed    time.Duration
	Done       bool
}

// Options tune a scan.
type Options struct {
	// Opaque reports directories that should be measured but shown as one
	// item, such as a Photos library.
	Opaque func(path, name string) bool
	// Workers is the number of parallel walkers; 0 picks a default.
	Workers int
}

// Scanner walks one tree.
type Scanner struct {
	Root *Dir

	opt     Options
	rootDev int32
	mu      sync.RWMutex // guards every Dir.dirs

	qmu     sync.Mutex
	qcond   *sync.Cond
	queue   []*Dir
	pending int

	inoMu  sync.Mutex
	inodes map[uint64]struct{}

	topMu  sync.Mutex
	top    fileHeap
	topMin atomic.Int64

	nFiles      atomic.Int64
	nDirs       atomic.Int64
	nUnreadable atomic.Int64
	start       time.Time
	elapsed     atomic.Int64
	done        chan struct{}
	now         time.Time
}

// New prepares a scan of root. Nothing is read until Start.
func New(root string, opt Options) (*Scanner, error) {
	root = filepath.Clean(root)
	var st syscall.Stat_t
	if err := syscall.Stat(root, &st); err != nil {
		return nil, &os.PathError{Op: "stat", Path: root, Err: err}
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return nil, &os.PathError{Op: "scan", Path: root, Err: syscall.ENOTDIR}
	}
	if opt.Workers <= 0 {
		opt.Workers = max(4, 2*runtime.NumCPU())
	}
	s := &Scanner{
		Root:    &Dir{Name: root},
		opt:     opt,
		rootDev: st.Dev,
		inodes:  map[uint64]struct{}{},
		done:    make(chan struct{}),
	}
	s.qcond = sync.NewCond(&s.qmu)
	return s, nil
}

// Start begins scanning in the background and returns at once.
func (s *Scanner) Start(ctx context.Context) {
	s.start = time.Now()
	s.now = s.start
	s.queue = append(s.queue, s.Root)
	s.pending = 1
	var wg sync.WaitGroup
	for i := 0; i < s.opt.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(ctx)
		}()
	}
	go func() {
		wg.Wait()
		s.elapsed.Store(int64(time.Since(s.start)))
		close(s.done)
	}()
}

// Run scans and waits for the result.
func (s *Scanner) Run(ctx context.Context) {
	s.Start(ctx)
	<-s.done
}

// Done is closed when the scan has finished.
func (s *Scanner) Done() <-chan struct{} { return s.done }

// Progress reports how far the scan has got.
func (s *Scanner) Progress() Progress {
	p := Progress{
		Files:      s.nFiles.Load(),
		Dirs:       s.nDirs.Load(),
		Unreadable: s.nUnreadable.Load(),
	}
	select {
	case <-s.done:
		p.Done = true
		p.Elapsed = time.Duration(s.elapsed.Load())
	default:
		if !s.start.IsZero() {
			p.Elapsed = time.Since(s.start)
		}
	}
	return p
}

func (s *Scanner) worker(ctx context.Context) {
	for {
		s.qmu.Lock()
		for len(s.queue) == 0 && s.pending > 0 {
			s.qcond.Wait()
		}
		if s.pending == 0 {
			s.qmu.Unlock()
			s.qcond.Broadcast()
			return
		}
		d := s.queue[len(s.queue)-1]
		s.queue = s.queue[:len(s.queue)-1]
		s.qmu.Unlock()

		var sub []*Dir
		if ctx.Err() == nil {
			sub = s.walk(d)
		}

		s.qmu.Lock()
		s.queue = append(s.queue, sub...)
		s.pending += len(sub) - 1
		s.qmu.Unlock()
		s.qcond.Broadcast()
	}
}

// add credits size and file count to d and every ancestor.
func add(d *Dir, size, files int64) {
	for ; d != nil; d = d.Parent {
		d.size.Add(size)
		d.files.Add(files)
	}
}

// seen reports whether a hard-linked inode was already counted.
func (s *Scanner) seen(ino uint64) bool {
	s.inoMu.Lock()
	defer s.inoMu.Unlock()
	if _, ok := s.inodes[ino]; ok {
		return true
	}
	s.inodes[ino] = struct{}{}
	return false
}

// walk reads one directory and returns the subdirectories still to visit.
func (s *Scanner) walk(d *Dir) []*Dir {
	path := d.Path()
	entries, err := readDir(path)
	s.nDirs.Add(1)
	if err != nil {
		d.Unreadable = true
		s.nUnreadable.Add(1)
		return nil
	}

	var (
		ownSize, ownFiles int64
		old               int32
		children, todo    []*Dir
	)
	for _, e := range entries {
		st := e.st
		size := st.Blocks * blockSize
		if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			if st.Nlink > 1 && s.seen(st.Ino) {
				size = 0
			}
			ownSize += size
			ownFiles++
			if st.Mode&syscall.S_IFMT == syscall.S_IFREG {
				atime := time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec)
				if s.now.Sub(atime) > staleAfter {
					old++
				}
				if size >= minTopFile && size > s.topMin.Load() {
					s.push(File{Dir: d, Name: e.name, Size: size, Length: st.Size, Atime: atime})
				}
			}
			continue
		}
		if st.Dev != s.rootDev {
			continue // another volume: not ours to count
		}
		child := &Dir{
			Name:    e.name,
			Parent:  d,
			ModTime: time.Unix(st.Mtimespec.Sec, st.Mtimespec.Nsec),
		}
		ownSize += size
		children = append(children, child)
		switch {
		case st.Flags&sfDataless != 0:
			child.Dataless = true
		case s.opt.Opaque != nil && s.opt.Opaque(filepath.Join(path, e.name), e.name):
			child.Bundle = true
			n, cnt := s.measure(filepath.Join(path, e.name), 0)
			child.size.Store(n)
			child.files.Store(cnt)
			ownSize += n
			ownFiles += cnt
			s.push(File{
				Dir: d, Name: e.name, Size: n, Length: n, Bundle: true, Count: cnt,
				Atime: time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec),
			})
		default:
			todo = append(todo, child)
		}
	}

	d.OldFiles = old
	s.mu.Lock()
	d.dirs = children
	s.mu.Unlock()
	s.nFiles.Add(ownFiles)
	add(d, ownSize, ownFiles)
	return todo
}

// measure adds up a subtree without keeping it in memory.
func (s *Scanner) measure(path string, depth int) (size, files int64) {
	if depth > maxSyncDepth {
		return 0, 0
	}
	entries, err := readDir(path)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		st := e.st
		n := st.Blocks * blockSize
		if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			if st.Nlink > 1 && s.seen(st.Ino) {
				n = 0
			}
			size += n
			files++
			continue
		}
		size += n
		if st.Dev != s.rootDev || st.Flags&sfDataless != 0 {
			continue
		}
		sn, sf := s.measure(filepath.Join(path, e.name), depth+1)
		size += sn
		files += sf
	}
	return size, files
}

type entry struct {
	name string
	st   *syscall.Stat_t
}

// readDir lists a directory with an lstat of every entry, unsorted.
func readDir(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	list, err := f.ReadDir(-1)
	f.Close()
	if err != nil && len(list) == 0 {
		return nil, err
	}
	out := make([]entry, 0, len(list))
	for _, e := range list {
		info, err := e.Info()
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		out = append(out, entry{name: e.Name(), st: st})
	}
	return out, nil
}

// Children returns the subdirectories of d, largest first.
func (s *Scanner) Children(d *Dir) []*Dir {
	s.mu.RLock()
	out := append([]*Dir(nil), d.dirs...)
	s.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Size() > out[j].Size() })
	return out
}

// Lookup finds the directory at an absolute path, or nil.
func (s *Scanner) Lookup(path string) *Dir {
	path = filepath.Clean(path)
	root := s.Root.Name
	if path == root {
		return s.Root
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || len(rel) >= 2 && rel[:2] == ".." {
		return nil
	}
	d := s.Root
	for _, part := range splitPath(rel) {
		var next *Dir
		s.mu.RLock()
		for _, c := range d.dirs {
			if c.Name == part {
				next = c
				break
			}
		}
		s.mu.RUnlock()
		if next == nil {
			return nil
		}
		d = next
	}
	return d
}

func splitPath(rel string) []string {
	return strings.Split(filepath.ToSlash(rel), "/")
}

// NodeModules counts the topmost node_modules directories below d.
func (s *Scanner) NodeModules(d *Dir) (count int, size int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var visit func(*Dir)
	visit = func(x *Dir) {
		for _, c := range x.dirs {
			if c.Name == nodeModules {
				count++
				size += c.Size()
				continue
			}
			visit(c)
		}
	}
	visit(d)
	return count, size
}

// --- largest files ---------------------------------------------------------

type fileHeap []File

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(File)) }
func (h *fileHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func (s *Scanner) push(f File) {
	s.topMu.Lock()
	defer s.topMu.Unlock()
	if len(s.top) < topCapacity {
		heap.Push(&s.top, f)
	} else if f.Size > s.top[0].Size {
		s.top[0] = f
		heap.Fix(&s.top, 0)
	} else {
		return
	}
	if len(s.top) == topCapacity {
		s.topMin.Store(s.top[0].Size)
	}
}

// TopFiles returns the largest files found so far, largest first.
func (s *Scanner) TopFiles() []File {
	s.topMu.Lock()
	out := append([]File(nil), s.top...)
	s.topMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// --- files of one directory -------------------------------------------------

// Entry is a non-directory item of a directory, listed on demand.
type Entry struct {
	Name   string
	Size   int64
	Length int64
	Atime  time.Time
	Link   bool
}

// List returns the files directly inside path, largest first. Directories
// are not included: they live in the tree.
func List(path string) ([]Entry, error) {
	entries, err := readDir(path)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		st := e.st
		if st.Mode&syscall.S_IFMT == syscall.S_IFDIR {
			continue
		}
		out = append(out, Entry{
			Name:   e.name,
			Size:   st.Blocks * blockSize,
			Length: st.Size,
			Atime:  time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec),
			Link:   st.Mode&syscall.S_IFMT == syscall.S_IFLNK,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// SizeOf measures a single path on disk: a file, or a whole directory.
func SizeOf(path string) int64 {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return 0
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return st.Blocks * blockSize
	}
	s := &Scanner{rootDev: st.Dev, inodes: map[uint64]struct{}{}}
	n, _ := s.measure(path, 0)
	return n + st.Blocks*blockSize
}
