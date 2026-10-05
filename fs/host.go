package fs

import (
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// HostFS provides a rooted filesystem backed by a host directory.
// It supports read-only or read-write access with strict symlink confinement:
// any symlink pointing outside the mount root is rejected.
type HostFS struct {
	mu       sync.RWMutex
	root     string // Cleaned absolute host path
	realRoot string // Symlink-evaluated canonical host path
	readOnly bool
}

// HostOption configures a HostFS.
type HostOption func(*HostFS)

// ReadOnly marks the host mount as read-only. All write operations fail with ErrPermission.
func ReadOnly() HostOption {
	return func(h *HostFS) {
		h.readOnly = true
	}
}

// ReadWrite marks the host mount as read-write.
func ReadWrite() HostOption {
	return func(h *HostFS) {
		h.readOnly = false
	}
}

// NewHostFS creates a HostFS anchored at hostDir.
// Traversal and symlink escapes outside hostDir are strictly prevented.
func NewHostFS(hostDir string, opts ...HostOption) (*HostFS, error) {
	abs, err := filepath.Abs(hostDir)
	if err != nil {
		return nil, fmt.Errorf("cordon: invalid host directory: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cordon: host directory error: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cordon: host path is not a directory: %s", abs)
	}

	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("cordon: failed to resolve host directory symlinks: %w", err)
	}

	h := &HostFS{
		root:     filepath.Clean(abs),
		realRoot: filepath.Clean(realRoot),
		readOnly: true, // Default to read-only for safety
	}
	for _, opt := range opts {
		opt(h)
	}

	return h, nil
}

// resolve maps a virtual rooted path into a confined host path.
// Returns an error if the path traverses or symlinks outside the root.
func (h *HostFS) resolve(p string) (string, error) {
	clean := Clean(p)
	rel := strings.TrimPrefix(clean, "/")
	target := filepath.Join(h.root, filepath.FromSlash(rel))

	// Lexicographical guard against .. escape
	if target != h.root && !strings.HasPrefix(target, h.root+string(filepath.Separator)) {
		return "", pathErr("resolve", p, iofs.ErrPermission)
	}

	// Symlink escape guard: evaluate canonical path
	realPath, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			// If target doesn't exist yet, evaluate parent directory to guard against
			// creating files through an escaping symlink directory.
			parent := filepath.Dir(target)
			realParent, perr := filepath.EvalSymlinks(parent)
			if perr == nil {
				if realParent != h.realRoot && !strings.HasPrefix(realParent, h.realRoot+string(filepath.Separator)) {
					return "", pathErr("resolve", p, iofs.ErrPermission)
				}
			}
			return target, nil
		}
		return "", err
	}

	if realPath != h.realRoot && !strings.HasPrefix(realPath, h.realRoot+string(filepath.Separator)) {
		return "", pathErr("resolve", p, iofs.ErrPermission)
	}

	return realPath, nil
}

// Open implements io/fs.FS for standard library compatibility.
func (h *HostFS) Open(name string) (iofs.File, error) {
	if !iofs.ValidPath(name) {
		return nil, pathErr("open", name, iofs.ErrInvalid)
	}
	return h.OpenFile("/"+name, os.O_RDONLY, 0)
}

// Stat returns metadata for name.
func (h *HostFS) Stat(name string) (iofs.FileInfo, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	target, err := h.resolve(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// ReadDir reads directory entries.
func (h *HostFS) ReadDir(name string) ([]iofs.DirEntry, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	target, err := h.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(target)
}

// ReadFile reads the full contents of a file.
func (h *HostFS) ReadFile(name string) ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	target, err := h.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

// OpenFile opens a file with specified flags and permissions.
func (h *HostFS) OpenFile(name string, flag int, perm iofs.FileMode) (File, error) {
	isWrite := (flag&os.O_WRONLY != 0) || (flag&os.O_RDWR != 0) || (flag&os.O_CREATE != 0) || (flag&os.O_TRUNC != 0) || (flag&os.O_APPEND != 0)

	if isWrite {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.readOnly {
			return nil, pathErr("open", name, iofs.ErrPermission)
		}
	} else {
		h.mu.RLock()
		defer h.mu.RUnlock()
	}

	target, err := h.resolve(name)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(target, flag, perm)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// WriteFile writes data to name.
func (h *HostFS) WriteFile(name string, data []byte, perm iofs.FileMode) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.readOnly {
		return pathErr("write", name, iofs.ErrPermission)
	}

	target, err := h.resolve(name)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, perm)
}

// Mkdir creates a single directory.
func (h *HostFS) Mkdir(name string, perm iofs.FileMode) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.readOnly {
		return pathErr("mkdir", name, iofs.ErrPermission)
	}

	target, err := h.resolve(name)
	if err != nil {
		return err
	}
	return os.Mkdir(target, perm)
}

// MkdirAll creates a directory and any missing parent directories.
func (h *HostFS) MkdirAll(name string, perm iofs.FileMode) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.readOnly {
		return pathErr("mkdir", name, iofs.ErrPermission)
	}

	target, err := h.resolve(name)
	if err != nil {
		return err
	}
	return os.MkdirAll(target, perm)
}

// Remove deletes a file or empty directory.
func (h *HostFS) Remove(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.readOnly {
		return pathErr("remove", name, iofs.ErrPermission)
	}

	target, err := h.resolve(name)
	if err != nil {
		return err
	}
	return os.Remove(target)
}

// RemoveAll deletes a path and any children.
func (h *HostFS) RemoveAll(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.readOnly {
		return pathErr("remove", name, iofs.ErrPermission)
	}

	target, err := h.resolve(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(target)
}

// Rename moves a file or directory.
func (h *HostFS) Rename(oldpath, newpath string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.readOnly {
		return pathErr("rename", oldpath, iofs.ErrPermission)
	}

	oldTarget, err := h.resolve(oldpath)
	if err != nil {
		return err
	}
	newTarget, err := h.resolve(newpath)
	if err != nil {
		return err
	}
	return os.Rename(oldTarget, newTarget)
}
