package shell

import (
	"io"
	iofs "io/fs"
	"time"

	"github.com/cordon-dev/cordon/fs"
)

const (
	devNullPath   = "/dev/null"
	devStdoutPath = "/dev/stdout"
	devStderrPath = "/dev/stderr"
)

// openDev returns a virtual device handle if name is a special device path.
func openDev(name string, stdout, stderr io.Writer) (devFile, bool) {
	switch fs.Clean(name) {
	case devNullPath:
		return devFile{name: "null", w: io.Discard}, true
	case devStdoutPath:
		return devFile{name: "stdout", w: stdout}, true
	case devStderrPath:
		return devFile{name: "stderr", w: stderr}, true
	default:
		return devFile{}, false
	}
}

// devFile implements both io.ReadWriteCloser (for shell redirections)
// and fs.File (for command operands like cat /dev/null).
type devFile struct {
	name string
	w    io.Writer
}

func (devFile) Read([]byte) (int, error)       { return 0, io.EOF }
func (f devFile) Write(p []byte) (int, error)  { return f.w.Write(p) }
func (devFile) Close() error                   { return nil }
func (devFile) Seek(int64, int) (int64, error) { return 0, nil }
func (f devFile) Stat() (iofs.FileInfo, error) { return devInfo{name: f.name}, nil }

// devInfo describes a synthetic character device.
type devInfo struct {
	name string
}

func (i devInfo) Name() string      { return i.name }
func (devInfo) Size() int64         { return 0 }
func (devInfo) Mode() iofs.FileMode { return iofs.ModeDevice | iofs.ModeCharDevice | 0o666 }
func (devInfo) ModTime() time.Time  { return time.Time{} }
func (devInfo) IsDir() bool         { return false }
func (devInfo) Sys() any            { return nil }

// devFS overlays virtual device paths on top of an underlying fs.FS.
type devFS struct {
	fs.FS
	stdout io.Writer
	stderr io.Writer
}

func newDevFS(base fs.FS, stdout, stderr io.Writer) devFS {
	return devFS{
		FS:     base,
		stdout: stdout,
		stderr: stderr,
	}
}

func (d devFS) device(name string) (devFile, bool) {
	return openDev(name, d.stdout, d.stderr)
}

func (d devFS) Open(name string) (iofs.File, error) {
	if f, ok := d.device(name); ok {
		return f, nil
	}
	return d.FS.Open(name)
}

func (d devFS) OpenFile(name string, flag int, perm iofs.FileMode) (fs.File, error) {
	if f, ok := d.device(name); ok {
		return f, nil
	}
	return d.FS.OpenFile(name, flag, perm)
}

func (d devFS) ReadFile(name string) ([]byte, error) {
	if _, ok := d.device(name); ok {
		return []byte{}, nil
	}
	return d.FS.ReadFile(name)
}

func (d devFS) Stat(name string) (iofs.FileInfo, error) {
	if f, ok := d.device(name); ok {
		return devInfo{name: f.name}, nil
	}
	return d.FS.Stat(name)
}
