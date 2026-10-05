package cordon

import (
	"context"
	"time"

	"github.com/cordon-dev/cordon/fs"
)

// Capabilities bundles policy-bound resource handles exposed to custom tools.
// Rather than giving custom code access to the Sandbox internals, Cordon provides
// narrow, capability-safe handles for files, networking, and timeouts.
type Capabilities struct {
	fs      fs.FS
	timeout time.Duration
}

// NewCapabilities constructs a Capabilities bundle with explicit handles.
func NewCapabilities(fsys fs.FS, timeout time.Duration) Capabilities {
	return Capabilities{
		fs:      fsys,
		timeout: timeout,
	}
}

// FS returns the capability-bound filesystem handle.
func (c Capabilities) FS() fs.FS {
	return c.fs
}

// Context derives a child context honoring the configured sandbox timeout (if any).
// Callers must invoke the returned CancelFunc when finished.
func (c Capabilities) Context(parent context.Context) (context.Context, context.CancelFunc) {
	if c.timeout > 0 {
		return context.WithTimeout(parent, c.timeout)
	}
	return context.WithCancel(parent)
}
