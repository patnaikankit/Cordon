package netpolicy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cordon-dev/cordon/netpolicy"
)

type mockResolver struct {
	ips map[string][]net.IP
	err error
}

func (m *mockResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if m.err != nil {
		return nil, m.err
	}
	if ips, ok := m.ips[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("host not found in mock: %s", host)
}

func TestPolicy_DenyByDefault(t *testing.T) {
	var zeroPolicy netpolicy.Policy

	// Zero policy should reject any host/port
	err := zeroPolicy.ValidateHostPort("example.com", 443)
	if err == nil {
		t.Fatalf("expected zero policy to deny host, got nil")
	}
	if !errors.Is(err, netpolicy.ErrNetworkDenied) {
		t.Errorf("expected ErrNetworkDenied, got %v", err)
	}

	u, _ := url.Parse("https://example.com/api")
	err = zeroPolicy.ValidateURL(u, "GET")
	if err == nil || !errors.Is(err, netpolicy.ErrNetworkDenied) {
		t.Errorf("expected ErrNetworkDenied on URL, got %v", err)
	}

	client := zeroPolicy.HTTPClient()
	_, err = client.Get("https://example.com")
	if err == nil {
		t.Errorf("expected zero policy client to fail request, got nil")
	}
}

func TestPolicy_AllowedHostAndPort(t *testing.T) {
	pol := netpolicy.New(
		netpolicy.AllowRule(netpolicy.Rule{
			Host:    "api.example.com",
			Ports:   []int{443},
			Methods: []string{"GET", "POST"},
		}),
		netpolicy.AllowRule(netpolicy.Rule{
			Host:    "*.cdn.example.org",
			Ports:   []int{80, 443},
			Methods: []string{"GET"},
		}),
	)

	// 1. Allowed exact host and port
	if err := pol.ValidateHostPort("api.example.com", 443); err != nil {
		t.Errorf("expected api.example.com:443 to be allowed: %v", err)
	}
	if err := pol.ValidateMethod("GET", "api.example.com"); err != nil {
		t.Errorf("expected GET to be allowed: %v", err)
	}
	if err := pol.ValidateMethod("POST", "api.example.com"); err != nil {
		t.Errorf("expected POST to be allowed: %v", err)
	}

	// 2. Disallowed method on allowed host
	if err := pol.ValidateMethod("DELETE", "api.example.com"); err == nil {
		t.Errorf("expected DELETE to be denied")
	} else if !errors.Is(err, netpolicy.ErrMethodNotAllowed) {
		t.Errorf("expected ErrMethodNotAllowed, got %v", err)
	}

	// 3. Disallowed port on allowed host
	if err := pol.ValidateHostPort("api.example.com", 80); err == nil {
		t.Errorf("expected port 80 to be denied on api.example.com")
	}

	// 4. Wildcard subdomain matching
	if err := pol.ValidateHostPort("images.cdn.example.org", 443); err != nil {
		t.Errorf("expected images.cdn.example.org to be allowed: %v", err)
	}
	if err := pol.ValidateHostPort("sub.deep.cdn.example.org", 80); err != nil {
		t.Errorf("expected sub.deep.cdn.example.org to be allowed: %v", err)
	}
}

func TestPolicy_DenyOverridesAllow(t *testing.T) {
	pol := netpolicy.New(
		// Allow all subdomains of internal.org
		netpolicy.AllowRule(netpolicy.Rule{
			Host: "*.internal.org",
		}),
		// But explicitly deny sensitive target
		netpolicy.DenyRule(netpolicy.Rule{
			Host: "secret.internal.org",
		}),
	)

	// Allowed subdomain
	if err := pol.ValidateHostPort("public.internal.org", 443); err != nil {
		t.Errorf("expected public.internal.org to be allowed: %v", err)
	}

	// Denied subdomain
	err := pol.ValidateHostPort("secret.internal.org", 443)
	if err == nil {
		t.Errorf("expected secret.internal.org to be denied")
	}
	if !errors.Is(err, netpolicy.ErrNetworkDenied) {
		t.Errorf("expected ErrNetworkDenied, got %v", err)
	}
}

func TestPolicy_SSRFProtection(t *testing.T) {
	pol := netpolicy.New(
		// Permissive host rule to verify IP layer blocks SSRF
		netpolicy.AllowRule(netpolicy.Rule{
			Host: "*",
		}),
	)

	ssrfTargets := []string{
		"127.0.0.1",
		"127.0.1.5",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254", // Cloud metadata service
		"0.0.0.0",
		"::1",
		"fc00::1",
		"fe80::1",
	}

	for _, target := range ssrfTargets {
		ip := net.ParseIP(target)
		if ip == nil {
			t.Fatalf("failed to parse IP: %s", target)
		}
		if err := pol.ValidateIP(ip); err == nil {
			t.Errorf("expected SSRF block for %s, got nil", target)
		} else if !errors.Is(err, netpolicy.ErrPrivateIPBlocked) {
			t.Errorf("expected ErrPrivateIPBlocked for %s, got %v", target, err)
		}
	}

	// Public IP should pass
	publicIP := net.ParseIP("93.184.216.34")
	if err := pol.ValidateIP(publicIP); err != nil {
		t.Errorf("expected public IP to pass validation: %v", err)
	}
}

func TestPolicy_DNSResolutionAndSSRF(t *testing.T) {
	// A mock resolver that resolves a public-looking domain name to a private IP (DNS rebinding / SSRF attempt)
	resolver := &mockResolver{
		ips: map[string][]net.IP{
			"malicious.com": {net.ParseIP("169.254.169.254")},
			"good.com":      {net.ParseIP("93.184.216.34")},
		},
	}

	pol := netpolicy.New(
		netpolicy.AllowRule(netpolicy.Rule{
			Host: "*",
		}),
		netpolicy.WithResolver(resolver),
	)

	// Dial malicious.com: should resolve to 169.254.169.254 and be blocked before dialing
	_, err := pol.DialContext(context.Background(), "tcp", "malicious.com:80")
	if err == nil {
		t.Fatalf("expected dial to malicious.com to be blocked by SSRF filter")
	}
	if !errors.Is(err, netpolicy.ErrPrivateIPBlocked) {
		t.Errorf("expected ErrPrivateIPBlocked, got %v", err)
	}
}

type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		conns:  make(chan net.Conn, 16),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c, ok := <-l.conns:
		if !ok {
			return nil, net.ErrClosed
		}
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("93.184.216.34"), Port: 80}
}

func (l *pipeListener) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	serverConn, clientConn := net.Pipe()
	l.conns <- serverConn
	return clientConn, nil
}

func TestPolicy_HTTPClientIntegration(t *testing.T) {
	listener := newPipeListener()
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Cordon-Auth") != "secret-token" {
				http.Error(w, "missing auth header", http.StatusUnauthorized)
				return
			}
			switch r.URL.Path {
			case "/ok":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("hello from test server"))
			case "/large":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(strings.Repeat("A", 1024)))
			case "/redirect":
				http.Redirect(w, r, "http://evil.attacker.com/steal", http.StatusFound)
			default:
				http.NotFound(w, r)
			}
		}),
	}
	go server.Serve(listener)
	defer listener.Close()

	pol := netpolicy.Policy{
		Allow: []netpolicy.Rule{
			{Host: "example.com", Ports: []int{80}},
			{Host: "evil.attacker.com"},
		},
		InjectedHeaders: map[string]string{
			"X-Cordon-Auth": "secret-token",
		},
		MaxResponseBytes: 128,
		Resolver: &mockResolver{
			ips: map[string][]net.IP{
				"example.com": {net.ParseIP("93.184.216.34")},
			},
		},
		Dialer: listener,
	}

	client := pol.HTTPClient()

	// 1. Successful request with boundary injected header
	resp, err := client.Get("http://example.com/ok")
	if err != nil {
		t.Fatalf("client.Get failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello from test server" {
		t.Errorf("got %q, want 'hello from test server'", string(body))
	}

	// 2. Response exceeding MaxResponseBytes
	resp, err = client.Get("http://example.com/large")
	if err != nil {
		t.Fatalf("client.Get large failed: %v", err)
	}
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err == nil {
		t.Errorf("expected ErrResponseTooLarge, got nil")
	} else if !errors.Is(err, netpolicy.ErrResponseTooLarge) {
		t.Errorf("expected ErrResponseTooLarge, got %v", err)
	}

	// 3. Cross-host redirect blocked
	_, err = client.Get("http://example.com/redirect")
	if err == nil {
		t.Errorf("expected cross-host redirect to be blocked")
	} else if !errors.Is(err, netpolicy.ErrCrossHostRedirectBlocked) {
		t.Errorf("expected ErrCrossHostRedirectBlocked, got %v", err)
	}
}
