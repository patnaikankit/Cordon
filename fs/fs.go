package fs

import (
	iofs "io/fs"
)

// FS is the capability-bound virtual filesystem interface for Cordon.
// It will be extended in Phase 2 to support write operations, path confinement, and quotas.
type FS interface {
	iofs.FS
}

// nopFS is a safe, empty read-only filesystem returning ErrNotExist for any file.
type nopFS struct{}

func (nopFS) Open(name string) (iofs.File, error) {
	return nil, &iofs.PathError{Op: "open", Path: name, Err: iofs.ErrNotExist}
}

// Nop returns a safe no-op filesystem that contains no files.
func Nop() FS {
	return nopFS{}
}
