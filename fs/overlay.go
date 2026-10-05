package fs

import (
	iofs "io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
)

// OverlayFS provides a Copy-on-Write (CoW) filesystem.
// All modifications (writes, file creations, directory creations, renames, and deletions)
// are kept in the in-memory upper layer (MemFS), leaving the lower layer completely pristine.
type OverlayFS struct {
	mu        sync.RWMutex
	upper     *MemFS
	lower     FS
	whiteouts map[string]bool
}

// NewOverlay creates an OverlayFS with upper as the modifiable in-memory layer
// and lower as the base read-only layer.
func NewOverlay(upper *MemFS, lower FS) *OverlayFS {
	if upper == nil {
		upper = NewMemFS()
	}
	return &OverlayFS{
		upper:     upper,
		lower:     lower,
		whiteouts: make(map[string]bool),
	}
}

// CoW creates a Copy-on-Write overlay on top of lower with an empty in-memory upper layer.
func CoW(lower FS) *OverlayFS {
	return NewOverlay(NewMemFS(), lower)
}

// isWhiteout reports whether path p or any ancestor directory has been whited out.
// Caller must hold o.mu (read or write).
func (o *OverlayFS) isWhiteout(p string) bool {
	p = Clean(p)
	cur := p
	for {
		if o.whiteouts[cur] {
			return true
		}
		parent := path.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return false
}

// clearWhiteout removes p and any descendants from the whiteout set.
// Caller must hold o.mu (write).
func (o *OverlayFS) clearWhiteout(p string) {
	p = Clean(p)
	delete(o.whiteouts, p)
	prefix := p + "/"
	for k := range o.whiteouts {
		if strings.HasPrefix(k, prefix) {
			delete(o.whiteouts, k)
		}
	}
}

// Open implements io/fs.FS for standard library compatibility.
func (o *OverlayFS) Open(name string) (iofs.File, error) {
	if !iofs.ValidPath(name) {
		return nil, pathErr("open", name, iofs.ErrInvalid)
	}
	return o.OpenFile("/"+name, os.O_RDONLY, 0)
}

// Stat returns metadata for name.
func (o *OverlayFS) Stat(name string) (iofs.FileInfo, error) {
	p := Clean(name)
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.isWhiteout(p) {
		return nil, pathErr("stat", name, iofs.ErrNotExist)
	}

	info, err := o.upper.Stat(name)
	if err == nil {
		return info, nil
	}
	if !errorsIsNotExist(err) {
		return nil, err
	}

	if o.lower == nil {
		return nil, pathErr("stat", name, iofs.ErrNotExist)
	}
	return o.lower.Stat(name)
}

// ReadDir reads directory entries, merging upper and lower and filtering whiteouts.
func (o *OverlayFS) ReadDir(name string) ([]iofs.DirEntry, error) {
	p := Clean(name)
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.isWhiteout(p) {
		return nil, pathErr("readdir", name, iofs.ErrNotExist)
	}

	merged := make(map[string]iofs.DirEntry)

	// 1. Read lower layer entries
	if o.lower != nil {
		lowerEntries, err := o.lower.ReadDir(name)
		if err == nil {
			for _, entry := range lowerEntries {
				childPath := Clean(path.Join(p, entry.Name()))
				if !o.isWhiteout(childPath) {
					merged[entry.Name()] = entry
				}
			}
		} else if !errorsIsNotExist(err) {
			return nil, err
		}
	}

	// 2. Read upper layer entries (upper overrides lower)
	upperEntries, err := o.upper.ReadDir(name)
	if err == nil {
		for _, entry := range upperEntries {
			merged[entry.Name()] = entry
		}
	} else if !errorsIsNotExist(err) {
		return nil, err
	}

	if len(merged) == 0 && (o.lower == nil || !o.dirExists(p)) {
		return nil, pathErr("readdir", name, iofs.ErrNotExist)
	}

	result := make([]iofs.DirEntry, 0, len(merged))
	for _, entry := range merged {
		result = append(result, entry)
	}

	slices.SortFunc(result, func(a, b iofs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	return result, nil
}

// ReadFile reads file contents.
func (o *OverlayFS) ReadFile(name string) ([]byte, error) {
	p := Clean(name)
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.isWhiteout(p) {
		return nil, pathErr("read", name, iofs.ErrNotExist)
	}

	data, err := o.upper.ReadFile(name)
	if err == nil {
		return data, nil
	}
	if !errorsIsNotExist(err) {
		return nil, err
	}

	if o.lower == nil {
		return nil, pathErr("read", name, iofs.ErrNotExist)
	}
	return o.lower.ReadFile(name)
}

// OpenFile opens a file, performing Copy-on-Write when write flags are set.
func (o *OverlayFS) OpenFile(name string, flag int, perm iofs.FileMode) (File, error) {
	p := Clean(name)
	isWrite := (flag&os.O_WRONLY != 0) || (flag&os.O_RDWR != 0) || (flag&os.O_CREATE != 0) || (flag&os.O_TRUNC != 0) || (flag&os.O_APPEND != 0)

	if !isWrite {
		o.mu.RLock()
		defer o.mu.RUnlock()

		if o.isWhiteout(p) {
			return nil, pathErr("open", name, iofs.ErrNotExist)
		}

		f, err := o.upper.OpenFile(name, flag, perm)
		if err == nil {
			return f, nil
		}
		if !errorsIsNotExist(err) {
			return nil, err
		}

		if o.lower == nil {
			return nil, pathErr("open", name, iofs.ErrNotExist)
		}
		return o.lower.OpenFile(name, flag, perm)
	}

	// Write path: mutate only the in-memory upper layer
	o.mu.Lock()
	defer o.mu.Unlock()

	o.clearWhiteout(p)

	// If file does not exist in upper yet, but exists in lower, copy it up!
	_, uerr := o.upper.Stat(name)
	if errorsIsNotExist(uerr) && o.lower != nil && (flag&os.O_TRUNC == 0) {
		if ldata, lerr := o.lower.ReadFile(name); lerr == nil {
			linfo, _ := o.lower.Stat(name)
			lperm := perm
			if linfo != nil {
				lperm = linfo.Mode().Perm()
			}
			o.ensureUpperParentsLocked(path.Dir(p))
			_ = o.upper.WriteFile(name, ldata, lperm)
		}
	}

	o.ensureUpperParentsLocked(path.Dir(p))
	return o.upper.OpenFile(name, flag, perm)
}

// WriteFile writes data into the in-memory upper layer.
func (o *OverlayFS) WriteFile(name string, data []byte, perm iofs.FileMode) error {
	p := Clean(name)
	o.mu.Lock()
	defer o.mu.Unlock()

	o.clearWhiteout(p)
	o.ensureUpperParentsLocked(path.Dir(p))
	return o.upper.WriteFile(name, data, perm)
}

// Mkdir creates a directory in the in-memory upper layer.
func (o *OverlayFS) Mkdir(name string, perm iofs.FileMode) error {
	p := Clean(name)
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.isWhiteout(p) && o.dirExists(p) {
		return pathErr("mkdir", name, iofs.ErrExist)
	}

	o.clearWhiteout(p)
	o.ensureUpperParentsLocked(path.Dir(p))
	return o.upper.Mkdir(name, perm)
}

// MkdirAll creates a directory and missing parents in the upper layer.
func (o *OverlayFS) MkdirAll(name string, perm iofs.FileMode) error {
	p := Clean(name)
	o.mu.Lock()
	defer o.mu.Unlock()

	o.clearWhiteout(p)
	o.ensureUpperParentsLocked(p)
	return o.upper.MkdirAll(name, perm)
}

// Remove deletes a file or empty directory by removing from upper and marking whiteout.
func (o *OverlayFS) Remove(name string) error {
	p := Clean(name)
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isWhiteout(p) {
		return pathErr("remove", name, iofs.ErrNotExist)
	}

	inUpper := false
	if _, err := o.upper.Stat(name); err == nil {
		inUpper = true
	}

	inLower := false
	if o.lower != nil {
		if _, err := o.lower.Stat(name); err == nil {
			inLower = true
		}
	}

	if !inUpper && !inLower {
		return pathErr("remove", name, iofs.ErrNotExist)
	}

	// If directory, check that it's empty
	if entries, err := o.readDirLocked(p); err == nil && len(entries) > 0 {
		return pathErr("remove", name, ErrNotEmpty)
	}

	if inUpper {
		if err := o.upper.Remove(name); err != nil {
			return err
		}
	}

	if inLower {
		o.whiteouts[p] = true
	}

	return nil
}

// RemoveAll deletes path and all contents from upper and whiteouts the subtree.
func (o *OverlayFS) RemoveAll(name string) error {
	p := Clean(name)
	o.mu.Lock()
	defer o.mu.Unlock()

	_ = o.upper.RemoveAll(name)
	o.whiteouts[p] = true
	return nil
}

// Rename renames oldpath to newpath, keeping all changes in the upper layer.
func (o *OverlayFS) Rename(oldpath, newpath string) error {
	oldP := Clean(oldpath)
	newP := Clean(newpath)

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isWhiteout(oldP) {
		return pathErr("rename", oldpath, iofs.ErrNotExist)
	}

	// Copy-on-write oldpath if only in lower
	if _, err := o.upper.Stat(oldpath); errorsIsNotExist(err) && o.lower != nil {
		if linfo, err := o.lower.Stat(oldpath); err == nil {
			if linfo.IsDir() {
				o.ensureUpperParentsLocked(oldP)
				_ = o.upper.Mkdir(oldpath, linfo.Mode().Perm())
			} else {
				if ldata, err := o.lower.ReadFile(oldpath); err == nil {
					o.ensureUpperParentsLocked(path.Dir(oldP))
					_ = o.upper.WriteFile(oldpath, ldata, linfo.Mode().Perm())
				}
			}
		}
	}

	o.ensureUpperParentsLocked(path.Dir(newP))
	o.clearWhiteout(newP)

	if err := o.upper.Rename(oldpath, newpath); err != nil {
		return err
	}

	// If oldpath existed in lower, white it out
	if o.lower != nil {
		if _, err := o.lower.Stat(oldpath); err == nil {
			o.whiteouts[oldP] = true
		}
	}

	return nil
}

func (o *OverlayFS) dirExists(p string) bool {
	if info, err := o.upper.Stat(p); err == nil && info.IsDir() {
		return true
	}
	if o.lower != nil {
		if info, err := o.lower.Stat(p); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func (o *OverlayFS) ensureUpperParentsLocked(p string) {
	if p == "/" || p == "." || p == "" {
		return
	}
	_ = o.upper.MkdirAll(p, 0755)
}

func (o *OverlayFS) readDirLocked(p string) ([]iofs.DirEntry, error) {
	merged := make(map[string]iofs.DirEntry)
	if o.lower != nil {
		if entries, err := o.lower.ReadDir(p); err == nil {
			for _, e := range entries {
				child := Clean(path.Join(p, e.Name()))
				if !o.isWhiteout(child) {
					merged[e.Name()] = e
				}
			}
		}
	}
	if entries, err := o.upper.ReadDir(p); err == nil {
		for _, e := range entries {
			merged[e.Name()] = e
		}
	}
	res := make([]iofs.DirEntry, 0, len(merged))
	for _, e := range merged {
		res = append(res, e)
	}
	return res, nil
}

func errorsIsNotExist(err error) bool {
	return os.IsNotExist(err) || iofs.ErrNotExist == err
}
