package cordon

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/netpolicy"
)

// Capabilities bundles policy-bound resource handles exposed to custom tools.
// Rather than giving custom code access to the Sandbox internals, Cordon provides
// narrow, capability-safe handles for files, networking, and timeouts.
type Capabilities struct {
	fs      fs.FS
	net     netpolicy.Policy
	timeout time.Duration
}

// NewCapabilities constructs a Capabilities bundle with explicit handles.
func NewCapabilities(fsys fs.FS, netPol netpolicy.Policy, timeout time.Duration) Capabilities {
	return Capabilities{
		fs:      fsys,
		net:     netPol,
		timeout: timeout,
	}
}

// FS returns the capability-bound filesystem handle.
func (c Capabilities) FS() fs.FS {
	return c.fs
}

// Network returns the sandbox network policy.
func (c Capabilities) Network() netpolicy.Policy {
	return c.net
}

// HTTPClient returns an http.Client bound to the sandbox's network policy.
func (c Capabilities) HTTPClient() *http.Client {
	return c.net.HTTPClient()
}

// DialContext dials outbound TCP connections under the sandbox's network policy.
func (c Capabilities) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return c.net.DialContext(ctx, network, address)
}

// Context derives a child context honoring the configured sandbox timeout (if any).
// Callers must invoke the returned CancelFunc when finished.
func (c Capabilities) Context(parent context.Context) (context.Context, context.CancelFunc) {
	if c.timeout > 0 {
		return context.WithTimeout(parent, c.timeout)
	}
	return context.WithCancel(parent)
}
