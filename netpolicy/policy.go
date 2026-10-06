package netpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Standard sentinel errors for network policy enforcement.
var (
	ErrNetworkDenied            = errors.New("cordon: network policy denied request")
	ErrPrivateIPBlocked         = errors.New("cordon: private/loopback IP address blocked (SSRF guard)")
	ErrCrossHostRedirectBlocked = errors.New("cordon: cross-host redirect blocked by network policy")
	ErrResponseTooLarge         = errors.New("cordon: response body exceeded MaxResponseBytes")
	ErrMethodNotAllowed         = errors.New("cordon: HTTP method not allowed by network policy")
)

// DefaultMaxResponseBytes is the default cap for HTTP response bodies (10 MB).
const DefaultMaxResponseBytes int64 = 10 * 1024 * 1024

// Rule specifies destination criteria for allowed or denied network traffic.
type Rule struct {
	// Host matches hostnames or IP strings.
	// Supports exact match ("api.example.com"), wildcard prefix ("*.example.com"),
	// or wildcard all ("*"). Matching is case-insensitive.
	Host string

	// Ports lists allowed or denied destination ports.
	// An empty slice matches all ports.
	Ports []int

	// Methods lists HTTP methods (e.g. "GET", "POST").
	// An empty slice matches all HTTP methods.
	Methods []string
}

// Resolver resolves hostnames to concrete IP addresses.
type Resolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// Policy specifies fine-grained outbound network controls.
// The zero value is deny-by-default (blocks all outbound network access).
type Policy struct {
	// Allow is the list of permitted destination rules.
	// Empty means deny-all (the safe default).
	Allow []Rule

	// Deny is the list of explicitly blocked destination rules.
	// Deny rules always take precedence over Allow rules.
	Deny []Rule

	// AllowPrivateIPs, if true, permits connections to loopback, private (RFC 1918),
	// link-local, and cloud metadata IP addresses.
	// Defaults to false (SSRF protection enabled by default!).
	AllowPrivateIPs bool

	// InjectedHeaders are headers attached automatically at the request boundary
	// to every outbound HTTP request (e.g. secure auth tokens).
	InjectedHeaders map[string]string

	// MaxResponseBytes caps the maximum response body size.
	// If 0, DefaultMaxResponseBytes is used.
	MaxResponseBytes int64

	// AllowCrossHostRedirects, if true, permits HTTP redirects to a different host.
	// Defaults to false (cross-host redirects blocked by default!).
	AllowCrossHostRedirects bool

	// Resolver overrides the default DNS resolver (primarily useful for testing).
	Resolver Resolver

	// Dialer overrides the underlying network dialer (useful for in-memory virtual pipes).
	Dialer Dialer
}

// Dialer establishes network connections.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// WithDialer sets a custom network dialer.
func WithDialer(d Dialer) Option {
	return func(p *Policy) {
		p.Dialer = d
	}
}

// New configures a Policy with specified options.
func New(opts ...Option) Policy {
	p := Policy{
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

// Option configures Policy options.
type Option func(*Policy)

// AllowRule appends an allow rule.
func AllowRule(r Rule) Option {
	return func(p *Policy) {
		p.Allow = append(p.Allow, r)
	}
}

// DenyRule appends a deny rule.
func DenyRule(r Rule) Option {
	return func(p *Policy) {
		p.Deny = append(p.Deny, r)
	}
}

// AllowHost is a convenience option for allowing a host on default HTTP/HTTPS ports (80, 443).
func AllowHost(host string, ports ...int) Option {
	return func(p *Policy) {
		p.Allow = append(p.Allow, Rule{Host: host, Ports: ports})
	}
}

// WithInjectedHeaders sets boundary-injected HTTP headers.
func WithInjectedHeaders(headers map[string]string) Option {
	return func(p *Policy) {
		p.InjectedHeaders = headers
	}
}

// WithMaxResponseBytes sets maximum response body size.
func WithMaxResponseBytes(maxBytes int64) Option {
	return func(p *Policy) {
		p.MaxResponseBytes = maxBytes
	}
}

// WithResolver sets a custom DNS resolver.
func WithResolver(r Resolver) Option {
	return func(p *Policy) {
		p.Resolver = r
	}
}

// WithAllowPrivateIPs controls private/loopback IP connectivity.
func WithAllowPrivateIPs(allow bool) Option {
	return func(p *Policy) {
		p.AllowPrivateIPs = allow
	}
}

// WithAllowCrossHostRedirects controls cross-host redirect behavior.
func WithAllowCrossHostRedirects(allow bool) Option {
	return func(p *Policy) {
		p.AllowCrossHostRedirects = allow
	}
}

// IsZero reports whether the policy is unconfigured / zero-value.
func (p Policy) IsZero() bool {
	return len(p.Allow) == 0 && len(p.Deny) == 0 && !p.AllowPrivateIPs && len(p.InjectedHeaders) == 0
}

// effectiveMaxResponseBytes reports the response byte limit.
func (p Policy) effectiveMaxResponseBytes() int64 {
	if p.MaxResponseBytes <= 0 {
		return DefaultMaxResponseBytes
	}
	return p.MaxResponseBytes
}

// ValidateHostPort checks whether outbound traffic to host and port is permitted by policy.
func (p Policy) ValidateHostPort(host string, port int) error {
	if p.IsZero() || len(p.Allow) == 0 {
		return fmt.Errorf("%w: zero policy denies all outbound traffic (%s:%d)", ErrNetworkDenied, host, port)
	}

	host = strings.TrimSpace(host)
	// Strip enclosing brackets if host is an IPv6 literal (e.g. "[::1]")
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")

	// SSRF protection: reject private/loopback/cloud-metadata IP literals and metadata hosts
	if !p.AllowPrivateIPs {
		if strings.EqualFold(host, "metadata.google.internal") {
			return fmt.Errorf("%w: cloud metadata host blocked: %s", ErrPrivateIPBlocked, host)
		}
		if ip := net.ParseIP(host); ip != nil && IsBlockedIP(ip) {
			return fmt.Errorf("%w: private/loopback/metadata IP blocked: %s", ErrPrivateIPBlocked, host)
		}
	}

	// 1. Check Deny rules first (Deny rules always take precedence)
	for _, rule := range p.Deny {
		if matchHost(rule.Host, host) && matchPort(rule.Ports, port) {
			return fmt.Errorf("%w: explicitly blocked by deny rule for %s:%d", ErrNetworkDenied, host, port)
		}
	}

	// 2. Check Allow rules (Deny-by-default)
	for _, rule := range p.Allow {
		if matchHost(rule.Host, host) && matchPort(rule.Ports, port) {
			return nil
		}
	}

	return fmt.Errorf("%w: host %s:%d not in allowlist", ErrNetworkDenied, host, port)
}

// ValidateMethod checks whether the given HTTP method is permitted for the target host.
func (p Policy) ValidateMethod(method, host string) error {
	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)

	// Check Deny rules
	for _, rule := range p.Deny {
		if matchHost(rule.Host, host) && matchMethod(rule.Methods, method) {
			return fmt.Errorf("%w: method %s blocked on %s", ErrMethodNotAllowed, method, host)
		}
	}

	// If any matching allow rule restricts methods, the method must be in it.
	matchedAllow := false
	methodPermitted := false
	for _, rule := range p.Allow {
		if matchHost(rule.Host, host) {
			matchedAllow = true
			if len(rule.Methods) == 0 || matchMethod(rule.Methods, method) {
				methodPermitted = true
				break
			}
		}
	}

	if matchedAllow && !methodPermitted {
		return fmt.Errorf("%w: method %s not allowed on %s", ErrMethodNotAllowed, method, host)
	}

	return nil
}

// ValidateURL checks whether an HTTP/HTTPS URL and method are permitted.
func (p Policy) ValidateURL(u *url.URL, method string) error {
	if u == nil {
		return fmt.Errorf("%w: nil URL", ErrNetworkDenied)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme %q (only http and https supported)", ErrNetworkDenied, scheme)
	}

	hostname := u.Hostname()
	port := 80
	if scheme == "https" {
		port = 443
	}
	if pStr := u.Port(); pStr != "" {
		if parsed, err := strconv.Atoi(pStr); err == nil {
			port = parsed
		}
	}

	if err := p.ValidateHostPort(hostname, port); err != nil {
		return err
	}

	return p.ValidateMethod(method, hostname)
}

// ValidateIP checks whether a resolved or concrete IP address violates SSRF protection.
func (p Policy) ValidateIP(ip net.IP) error {
	if !p.AllowPrivateIPs && IsBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrPrivateIPBlocked, ip.String())
	}
	return nil
}

func matchHost(pattern, host string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)

	if pattern == host {
		return true
	}

	// Wildcard domain matching: "*.example.com"
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if strings.HasSuffix(host, suffix) {
			return true
		}
		// Also match exact root if pattern is "*.example.com" and host is "example.com"
		if host == pattern[2:] {
			return true
		}
	}

	return false
}

func matchPort(ports []int, port int) bool {
	if len(ports) == 0 {
		return true
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

func matchMethod(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, m := range methods {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}
