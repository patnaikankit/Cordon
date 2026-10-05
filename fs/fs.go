package fs

import (
	"errors"
	"io"
	iofs "io/fs"
)

// FS is a write-capable filesystem that extends standard io/fs.FS.
// By embedding io/fs.FS, it composes seamlessly with fs.WalkDir, fs.ReadFile,
// http.FS, and testing/fstest. The additional methods provide full write,
// directory creation, and manipulation capabilities.
type FS interface {
	iofs.FS

	Stat(name string) (iofs.FileInfo, error)
	ReadDir(name string) ([]iofs.DirEntry, error)
	ReadFile(name string) ([]byte, error)

	OpenFile(name string, flag int, perm iofs.FileMode) (File, error)
	Mkdir(name string, perm iofs.FileMode) error
	MkdirAll(name string, perm iofs.FileMode) error
	Remove(name string) error
	RemoveAll(name string) error
	Rename(oldpath, newpath string) error
	WriteFile(name string, data []byte, perm iofs.FileMode) error
}

// File represents an open file.
// Its read side satisfies io/fs.File; write and seek support allow it to back
// shell redirections, file modifications, and streaming.
type File interface {
	iofs.File
	io.Writer
	io.Seeker
}

// Limits bounds how many bytes an FS may hold. A zero field means unlimited.
type Limits struct {
	// MaxTotalBytes caps the combined sum of all file contents in the filesystem.
	MaxTotalBytes int64
	// MaxFileBytes caps the size of any single file.
	MaxFileBytes int64
}

// Sentinel errors returned by filesystem operations, wrapped in *io/fs.PathError
// so callers can use errors.Is against standard io/fs or os errors.
var (
	ErrNotDir       = errors.New("not a directory")
	ErrIsDir        = errors.New("is a directory")
	ErrNotEmpty     = errors.New("directory not empty")
	ErrFileTooLarge = errors.New("file too large")
	ErrNoSpace      = errors.New("no space left on device")
	ErrBadFD        = errors.New("bad file descriptor")
)

func pathErr(op, p string, err error) error {
	return &iofs.PathError{Op: op, Path: p, Err: err}
}
