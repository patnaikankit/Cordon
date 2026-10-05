package netpolicy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DialContext connects to address using TCP, enforcing all host, port, IP, and SSRF rules.
// DNS resolution is performed first, all resolved IPs are validated against the IP blocklist,
// and the TCP connection is pinned directly to a validated concrete IP to prevent DNS rebinding.
func (p Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("%w: unsupported network %q (only tcp permitted)", ErrNetworkDenied, network)
	}

	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid address %q: %v", ErrNetworkDenied, address, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid port %q", ErrNetworkDenied, portStr)
	}

	// 1. Validate hostname/port against Allow and Deny rules
	if err := p.ValidateHostPort(host, port); err != nil {
		return nil, err
	}

	// 2. Resolve to concrete IPs
	var ips []net.IP
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		ips = []net.IP{ip}
	} else {
		res := p.Resolver
		if res == nil {
			res = net.DefaultResolver
		}
		resolved, err := res.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("cordon: DNS resolution failed for %q: %w", host, err)
		}
		if len(resolved) == 0 {
			return nil, fmt.Errorf("cordon: no IP addresses found for host %q", host)
		}
		ips = resolved
	}

	// 3. SSRF Check: Every resolved IP must pass validation.
	// Rejecting if ANY returned IP is private or loopback prevents dual-homed DNS tricks.
	for _, ip := range ips {
		if err := p.ValidateIP(ip); err != nil {
			return nil, err
		}
	}

	// 4. Pin dial to the first validated IP address to prevent DNS rebinding attacks.
	targetIP := ips[0]
	targetAddr := net.JoinHostPort(targetIP.String(), portStr)

	if p.Dialer != nil {
		return p.Dialer.DialContext(ctx, network, targetAddr)
	}

	d := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return d.DialContext(ctx, network, targetAddr)
}

// HTTPClient returns an http.Client strictly bound to this Policy.
// The client enforces DNS resolution validation, SSRF blocking, request boundary header injection,
// cross-host redirect guards, and response size limits.
func (p Policy) HTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return p.DialContext(ctx, network, address)
		},
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	rt := &policyRoundTripper{
		policy:    p,
		transport: transport,
	}

	return &http.Client{
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("cordon: stopped after 10 redirects")
			}
			if len(via) > 0 {
				origHost := via[0].URL.Hostname()
				newHost := req.URL.Hostname()
				if !strings.EqualFold(origHost, newHost) {
					if !p.AllowCrossHostRedirects {
						return fmt.Errorf("%w: %s -> %s", ErrCrossHostRedirectBlocked, origHost, newHost)
					}
					// Even if cross-host redirects are permitted, re-validate the new destination
					if err := p.ValidateURL(req.URL, req.Method); err != nil {
						return err
					}
					// Strip sensitive credentials on cross-host redirect
					req.Header.Del("Authorization")
					req.Header.Del("Cookie")
				}
			}
			return nil
		},
	}
}

// policyRoundTripper wraps an http.RoundTripper with header injection and response body bounds.
type policyRoundTripper struct {
	policy    Policy
	transport http.RoundTripper
}

func (rt *policyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. Validate method and URL
	if err := rt.policy.ValidateURL(req.URL, req.Method); err != nil {
		return nil, err
	}

	// 2. Clone request to inject boundary headers without mutating caller's original request
	clonedReq := req.Clone(req.Context())
	if clonedReq.Header == nil {
		clonedReq.Header = make(http.Header)
	}

	for k, v := range rt.policy.InjectedHeaders {
		clonedReq.Header.Set(k, v)
	}

	resp, err := rt.transport.RoundTrip(clonedReq)
	if err != nil {
		return nil, err
	}

	// 3. Wrap response body with size limit guard
	maxBytes := rt.policy.effectiveMaxResponseBytes()
	resp.Body = &boundedReadCloser{
		rc:       resp.Body,
		max:      maxBytes,
		consumed: 0,
	}

	return resp, nil
}

// boundedReadCloser wraps an io.ReadCloser and errors when reads exceed max bytes.
type boundedReadCloser struct {
	rc       io.ReadCloser
	max      int64
	consumed int64
}

func (b *boundedReadCloser) Read(p []byte) (int, error) {
	if b.max > 0 && b.consumed >= b.max {
		return 0, ErrResponseTooLarge
	}

	toRead := p
	if b.max > 0 {
		remaining := b.max - b.consumed
		if int64(len(p)) > remaining {
			toRead = p[:remaining]
		}
	}

	n, err := b.rc.Read(toRead)
	b.consumed += int64(n)

	if b.max > 0 && b.consumed >= b.max && err == nil {
		// Attempt reading one extra byte to detect if payload exceeds limit
		var probe [1]byte
		pn, perr := b.rc.Read(probe[:])
		if pn > 0 {
			return n, ErrResponseTooLarge
		}
		if perr != nil && perr != io.EOF {
			return n, perr
		}
	}

	return n, err
}

func (b *boundedReadCloser) Close() error {
	return b.rc.Close()
}
