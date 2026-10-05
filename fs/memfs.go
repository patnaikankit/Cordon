package fs

import (
	"io"
	iofs "io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// defaultMaxFileBytes bounds a single in-memory file when no explicit
// MaxFileBytes is configured. This backstop prevents sparse writes
// (e.g. seeking to math.MaxInt64 and writing 1 byte) from allocating
// giant buffers and exhausting host memory.
const defaultMaxFileBytes = 256 << 20 // 256 MiB

// memNode represents a single file or directory node in MemFS.
type memNode struct {
	mode    iofs.FileMode
	data    []byte
	modTime time.Time
}

func (n *memNode) isDir() bool {
	return n.mode.IsDir()
}

// MemFS is a thread-safe, purely in-memory virtual filesystem.
// It stores nothing on the host and strictly bounds byte allocations.
type MemFS struct {
	mu       sync.RWMutex
	nodes    map[string]*memNode
	limits   Limits
	total    int64
	inFlight int64 // bytes buffered by open uncommitted write handles
}

// MemOption configures a MemFS instance at construction.
type MemOption func(*MemFS)

// WithLimits sets byte capacity limits on the filesystem.
func WithLimits(l Limits) MemOption {
	return func(m *MemFS) {
		m.limits = l
	}
}

// WithFiles seeds the filesystem with an initial map of file paths to content strings.
func WithFiles(files map[string]string) MemOption {
	return func(m *MemFS) {
		for name, content := range files {
			p := Clean(name)
			_ = m.mkdirAll(path.Dir(p), 0o755)
			m.nodes[p] = &memNode{
				mode:    0o644,
				data:    []byte(content),
				modTime: time.Now(),
			}
			m.total += int64(len(content))
		}
	}
}

// NewMemFS creates an empty in-memory filesystem with a virtual root directory.
func NewMemFS(opts ...MemOption) *MemFS {
	m := &MemFS{
		nodes: make(map[string]*memNode),
	}
	m.nodes["/"] = &memNode{
		mode:    iofs.ModeDir | 0o755,
		modTime: time.Now(),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// mkdirAll creates path p and any missing parent directories.
// Must be called with m.mu held.
func (m *MemFS) mkdirAll(p string, perm iofs.FileMode) error {
	p = Clean(p)
	if n, ok := m.nodes[p]; ok {
		if !n.isDir() {
			return pathErr("mkdir", p, ErrNotDir)
		}
		return nil
	}
	parent := path.Dir(p)
	if parent != p {
		if err := m.mkdirAll(parent, perm); err != nil {
			return err
		}
	}
	m.nodes[p] = &memNode{
		mode:    iofs.ModeDir | perm,
		modTime: time.Now(),
	}
	return nil
}

// readDirLocked lists direct children of dir. Must be called with m.mu held.
func (m *MemFS) readDirLocked(dir string) []iofs.DirEntry {
	dir = Clean(dir)
	prefix := dir
	if prefix != "/" {
		prefix += "/"
	}
	var entries []iofs.DirEntry
	for p, n := range m.nodes {
		if p == dir || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		entries = append(entries, memDirEntry{
			info: memFileInfo{
				name:    rest,
				size:    int64(len(n.data)),
				mode:    n.mode,
				modTime: n.modTime,
			},
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries
}

// Open implements standard io/fs.FS. It follows standard unrooted path conventions,
// where "." denotes the filesystem root. This guarantees compatibility with
// testing/fstest, fs.WalkDir, and io/fs utilities.
func (m *MemFS) Open(name string) (iofs.File, error) {
	if !iofs.ValidPath(name) {
		return nil, pathErr("open", name, iofs.ErrInvalid)
	}
	target := "/"
	if name != "." {
		target = "/" + name
	}
	return m.OpenFile(target, os.O_RDONLY, 0)
}

// OpenFile opens the named file with standard flags and permissions.
// The path may be relative or absolute; it is always normalized into the virtual root.
func (m *MemFS) OpenFile(name string, flag int, perm iofs.FileMode) (File, error) {
	p := Clean(name)
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[p]
	if ok && n.isDir() {
		if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
			return nil, pathErr("open", name, ErrIsDir)
		}
		return &dirFile{
			name:    path.Base(p),
			entries: m.readDirLocked(p),
			mode:    n.mode,
			modTime: n.modTime,
		}, nil
	}

	switch {
	case !ok:
		if flag&os.O_CREATE == 0 {
			return nil, pathErr("open", name, iofs.ErrNotExist)
		}
		parent := path.Dir(p)
		pn, pok := m.nodes[parent]
		if !pok || !pn.isDir() {
			return nil, pathErr("open", name, iofs.ErrNotExist)
		}
		n = &memNode{
			mode:    perm.Perm(),
			modTime: time.Now(),
		}
		m.nodes[p] = n
	case flag&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL:
		return nil, pathErr("open", name, iofs.ErrExist)
	}

	f := &regFile{
		m:       m,
		path:    p,
		name:    path.Base(p),
		perm:    n.mode,
		modTime: n.modTime,
	}
	f.readable = flag&os.O_WRONLY == 0
	f.writable = flag&(os.O_WRONLY|os.O_RDWR) != 0

	switch {
	case flag&os.O_TRUNC != 0:
		f.dirty = true
	case flag&os.O_APPEND != 0:
		f.buf = append([]byte(nil), n.data...)
		f.off = int64(len(f.buf))
	default:
		f.buf = append([]byte(nil), n.data...)
	}

	return f, nil
}

// Stat returns file information for name.
func (m *MemFS) Stat(name string) (iofs.FileInfo, error) {
	p := Clean(name)
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[p]
	if !ok {
		return nil, pathErr("stat", name, iofs.ErrNotExist)
	}
	return memFileInfo{
		name:    path.Base(p),
		size:    int64(len(n.data)),
		mode:    n.mode,
		modTime: n.modTime,
	}, nil
}

// ReadDir reads the directory named by name, returning sorted entries.
func (m *MemFS) ReadDir(name string) ([]iofs.DirEntry, error) {
	p := Clean(name)
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[p]
	if !ok {
		return nil, pathErr("open", name, iofs.ErrNotExist)
	}
	if !n.isDir() {
		return nil, pathErr("read", name, ErrNotDir)
	}
	return m.readDirLocked(p), nil
}

// ReadFile returns the full contents of the file at name.
func (m *MemFS) ReadFile(name string) ([]byte, error) {
	p := Clean(name)
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[p]
	if !ok {
		return nil, pathErr("open", name, iofs.ErrNotExist)
	}
	if n.isDir() {
		return nil, pathErr("read", name, ErrIsDir)
	}
	return append([]byte(nil), n.data...), nil
}

// WriteFile writes data to name with given perm, replacing any existing content.
func (m *MemFS) WriteFile(name string, data []byte, perm iofs.FileMode) error {
	p := Clean(name)
	m.mu.Lock()
	defer m.mu.Unlock()

	parent := path.Dir(p)
	if pn, ok := m.nodes[parent]; !ok || !pn.isDir() {
		return pathErr("open", name, iofs.ErrNotExist)
	}

	var old int64
	if ex, ok := m.nodes[p]; ok {
		if ex.isDir() {
			return pathErr("open", name, ErrIsDir)
		}
		old = int64(len(ex.data))
	}

	if err := m.checkBytes(p, old, int64(len(data))); err != nil {
		return err
	}

	m.nodes[p] = &memNode{
		mode:    perm.Perm(),
		data:    append([]byte(nil), data...),
		modTime: time.Now(),
	}
	m.total += int64(len(data)) - old
	return nil
}

// checkBytes verifies that writing newLen bytes to p respects limits.
// Caller must hold m.mu.
func (m *MemFS) checkBytes(p string, old, newLen int64) error {
	if m.limits.MaxFileBytes > 0 && newLen > m.limits.MaxFileBytes {
		return pathErr("write", p, ErrFileTooLarge)
	}
	if m.limits.MaxTotalBytes > 0 && m.total-old+m.inFlight+newLen > m.limits.MaxTotalBytes {
		return pathErr("write", p, ErrNoSpace)
	}
	return nil
}

// Mkdir creates a single directory at name.
func (m *MemFS) Mkdir(name string, perm iofs.FileMode) error {
	p := Clean(name)
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.nodes[p]; ok {
		return pathErr("mkdir", name, iofs.ErrExist)
	}
	parent := path.Dir(p)
	if pn, ok := m.nodes[parent]; !ok || !pn.isDir() {
		return pathErr("mkdir", name, iofs.ErrNotExist)
	}
	m.nodes[p] = &memNode{
		mode:    iofs.ModeDir | perm.Perm(),
		modTime: time.Now(),
	}
	return nil
}

// MkdirAll creates name and all necessary parent directories.
func (m *MemFS) MkdirAll(name string, perm iofs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mkdirAll(Clean(name), perm.Perm())
}

// Remove deletes a file or empty directory.
func (m *MemFS) Remove(name string) error {
	p := Clean(name)
	if p == "/" {
		return pathErr("remove", name, iofs.ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[p]
	if !ok {
		return pathErr("remove", name, iofs.ErrNotExist)
	}
	if n.isDir() && len(m.readDirLocked(p)) > 0 {
		return pathErr("remove", name, ErrNotEmpty)
	}
	m.total -= int64(len(n.data))
	delete(m.nodes, p)
	return nil
}

// RemoveAll deletes name and all its children.
func (m *MemFS) RemoveAll(name string) error {
	p := Clean(name)
	if p == "/" {
		return pathErr("remove", name, iofs.ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := p + "/"
	for k, n := range m.nodes {
		if k == p || strings.HasPrefix(k, prefix) {
			m.total -= int64(len(n.data))
			delete(m.nodes, k)
		}
	}
	return nil
}

// Rename moves oldpath to newpath, moving whole subtrees if oldpath is a directory.
func (m *MemFS) Rename(oldpath, newpath string) error {
	op, np := Clean(oldpath), Clean(newpath)
	if op == "/" || np == "/" {
		return pathErr("rename", oldpath, iofs.ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[op]
	if !ok {
		return pathErr("rename", oldpath, iofs.ErrNotExist)
	}
	if op == np {
		return nil
	}
	if n.isDir() && strings.HasPrefix(np, op+"/") {
		return pathErr("rename", oldpath, iofs.ErrInvalid)
	}
	parent := path.Dir(np)
	if pn, pok := m.nodes[parent]; !pok || !pn.isDir() {
		return pathErr("rename", newpath, iofs.ErrNotExist)
	}
	if ex, ok := m.nodes[np]; ok {
		if ex.isDir() {
			return pathErr("rename", newpath, ErrIsDir)
		}
		m.total -= int64(len(ex.data))
	}
	if n.isDir() {
		oldPrefix := op + "/"
		var keys []string
		for k := range m.nodes {
			if strings.HasPrefix(k, oldPrefix) {
				keys = append(keys, k)
			}
		}
		for _, k := range keys {
			m.nodes[np+strings.TrimPrefix(k, op)] = m.nodes[k]
			delete(m.nodes, k)
		}
	}
	m.nodes[np] = n
	delete(m.nodes, op)
	return nil
}

// regFile is an open regular file backed by an in-memory buffer.
// Changes are committed to the filesystem when Close() is called.
type regFile struct {
	m        *MemFS
	path     string
	name     string
	perm     iofs.FileMode
	modTime  time.Time
	buf      []byte
	off      int64
	reserved int64
	readable bool
	writable bool
	dirty    bool
	closed   bool
}

func (f *regFile) Read(p []byte) (int, error) {
	if !f.readable {
		return 0, pathErr("read", f.path, ErrBadFD)
	}
	if f.off >= int64(len(f.buf)) {
		return 0, io.EOF
	}
	n := copy(p, f.buf[f.off:])
	f.off += int64(n)
	return n, nil
}

func (f *regFile) Write(p []byte) (int, error) {
	if !f.writable {
		return 0, pathErr("write", f.path, ErrBadFD)
	}
	end := f.off + int64(len(p))
	// Guard against integer overflow on seek+write
	if end < f.off {
		return 0, pathErr("write", f.path, ErrFileTooLarge)
	}

	maxFile := f.m.limits.MaxFileBytes
	if maxFile == 0 {
		maxFile = defaultMaxFileBytes
	}
	if end > maxFile {
		return 0, pathErr("write", f.path, ErrFileTooLarge)
	}

	if err := f.reserve(end); err != nil {
		return 0, err
	}

	if int64(len(f.buf)) < end {
		grown := make([]byte, end)
		copy(grown, f.buf)
		f.buf = grown
	}
	copy(f.buf[f.off:], p)
	f.off = end
	f.dirty = true
	return len(p), nil
}

func (f *regFile) reserve(newLen int64) error {
	if newLen <= f.reserved {
		return nil
	}
	f.m.mu.Lock()
	defer f.m.mu.Unlock()

	if f.m.limits.MaxTotalBytes > 0 {
		var old int64
		if ex, ok := f.m.nodes[f.path]; ok {
			old = int64(len(ex.data))
		}
		projected := f.m.total - old + (f.m.inFlight - f.reserved) + newLen
		if projected > f.m.limits.MaxTotalBytes {
			return pathErr("write", f.path, ErrNoSpace)
		}
	}
	f.m.inFlight += newLen - f.reserved
	f.reserved = newLen
	return nil
}

func (f *regFile) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = f.off + offset
	case io.SeekEnd:
		abs = int64(len(f.buf)) + offset
	default:
		return 0, pathErr("seek", f.path, iofs.ErrInvalid)
	}
	if abs < 0 {
		return 0, pathErr("seek", f.path, iofs.ErrInvalid)
	}
	f.off = abs
	return abs, nil
}

func (f *regFile) Stat() (iofs.FileInfo, error) {
	return memFileInfo{
		name:    f.name,
		size:    int64(len(f.buf)),
		mode:    f.perm,
		modTime: f.modTime,
	}, nil
}

func (f *regFile) Close() error {
	if f.closed {
		return pathErr("close", f.path, iofs.ErrClosed)
	}
	f.closed = true
	if !f.writable {
		return nil
	}

	f.m.mu.Lock()
	defer f.m.mu.Unlock()

	f.m.inFlight -= f.reserved
	f.reserved = 0
	if !f.dirty {
		return nil
	}

	var old int64
	if ex, ok := f.m.nodes[f.path]; ok {
		old = int64(len(ex.data))
	}
	if err := f.m.checkBytes(f.path, old, int64(len(f.buf))); err != nil {
		return err
	}
	f.m.nodes[f.path] = &memNode{
		mode:    f.perm,
		data:    f.buf,
		modTime: time.Now(),
	}
	f.m.total += int64(len(f.buf)) - old
	return nil
}

// dirFile represents an open directory satisfying io/fs.ReadDirFile.
type dirFile struct {
	name    string
	entries []iofs.DirEntry
	mode    iofs.FileMode
	modTime time.Time
	off     int
}

func (d *dirFile) Read([]byte) (int, error)       { return 0, pathErr("read", d.name, ErrIsDir) }
func (d *dirFile) Write([]byte) (int, error)      { return 0, pathErr("write", d.name, ErrIsDir) }
func (d *dirFile) Seek(int64, int) (int64, error) { return 0, pathErr("seek", d.name, ErrIsDir) }
func (d *dirFile) Close() error                   { return nil }
func (d *dirFile) Stat() (iofs.FileInfo, error) {
	return memFileInfo{name: d.name, mode: d.mode, modTime: d.modTime}, nil
}

func (d *dirFile) ReadDir(n int) ([]iofs.DirEntry, error) {
	if n <= 0 {
		rest := d.entries[d.off:]
		d.off = len(d.entries)
		return rest, nil
	}
	if d.off >= len(d.entries) {
		return nil, io.EOF
	}
	end := min(d.off+n, len(d.entries))
	rest := d.entries[d.off:end]
	d.off = end
	return rest, nil
}

// memFileInfo implements io/fs.FileInfo.
type memFileInfo struct {
	name    string
	size    int64
	mode    iofs.FileMode
	modTime time.Time
}

func (i memFileInfo) Name() string        { return i.name }
func (i memFileInfo) Size() int64         { return i.size }
func (i memFileInfo) Mode() iofs.FileMode { return i.mode }
func (i memFileInfo) ModTime() time.Time  { return i.modTime }
func (i memFileInfo) IsDir() bool         { return i.mode.IsDir() }
func (i memFileInfo) Sys() any            { return nil }

// memDirEntry implements io/fs.DirEntry.
type memDirEntry struct {
	info memFileInfo
}

func (e memDirEntry) Name() string                 { return e.info.name }
func (e memDirEntry) IsDir() bool                  { return e.info.IsDir() }
func (e memDirEntry) Type() iofs.FileMode          { return e.info.mode.Type() }
func (e memDirEntry) Info() (iofs.FileInfo, error) { return e.info, nil }

// Compile-time interface checks
var (
	_ FS               = (*MemFS)(nil)
	_ File             = (*regFile)(nil)
	_ iofs.ReadDirFile = (*dirFile)(nil)
)
