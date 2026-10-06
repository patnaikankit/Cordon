package security_test

import (
	"context"
	"encoding/json"
	"errors"
	iofs "io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
	"github.com/cordon-dev/cordon/netpolicy"
	"github.com/cordon-dev/cordon/script"
)

// Invariant 1: Path Traversal Confinement
func TestAdversarial_PathTraversal(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/app/secret.txt": "sensitive-data",
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       mem,
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	ctx := context.Background()

	traversalPayloads := []string{
		"cat /../../../etc/passwd",
		"cat //..//..//etc//passwd",
		"cat /app/../app/../../etc/shadow",
		"cat /app/secret.txt/../../../../root/.ssh/id_rsa",
		"cat ../../../../../../../etc/passwd",
		"ls -la /../../../../",
	}

	for _, payload := range traversalPayloads {
		res, err := sb.ExecBash(ctx, payload)
		if err != nil {
			t.Fatalf("unexpected dispatch error for %q: %v", payload, err)
		}
		// If command ran cat, it must not return host content
		if strings.Contains(res.Stdout, "root:x:") || strings.Contains(res.Stdout, "BEGIN OPENSSH PRIVATE KEY") {
			t.Fatalf("SECURITY VIOLATION: traversal payload %q leaked host files! Output:\n%s", payload, res.Stdout)
		}
	}

	// Verify fs.Clean normalization explicitly
	dirtyPaths := []string{
		"/../../../etc/passwd",
		"//..//..//etc",
		"/a/b/../../../../../../c",
		"/app/././../../secret",
	}
	for _, dp := range dirtyPaths {
		cleaned := fs.Clean(dp)
		if !strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "..") {
			t.Fatalf("SECURITY VIOLATION: fs.Clean failed to normalize %q -> %q", dp, cleaned)
		}
	}
}

// Invariant 2: Host Mount Symlink Confinement
func TestAdversarial_SymlinkEscape(t *testing.T) {
	tempDir := t.TempDir()
	mountRoot := filepath.Join(tempDir, "mount")
	_ = os.Mkdir(mountRoot, 0o755)

	// Create a secret file outside the mount root
	hostSecret := filepath.Join(tempDir, "host_secret.txt")
	_ = os.WriteFile(hostSecret, []byte("host-secret-token"), 0o644)

	// Create an escaping symlink inside the mount pointing to the secret outside
	escapeLink := filepath.Join(mountRoot, "escape_link.txt")
	_ = os.Symlink(hostSecret, escapeLink)

	// Create a directory symlink inside the mount pointing to /etc or parent
	escapeDirLink := filepath.Join(mountRoot, "escape_dir")
	_ = os.Symlink(tempDir, escapeDirLink)

	hfs, err := fs.NewHostFS(mountRoot, fs.ReadOnly())
	if err != nil {
		t.Fatalf("failed to create HostFS: %v", err)
	}

	// 1. Attempt to read file through escaping symlink
	_, err = hfs.ReadFile("/escape_link.txt")
	if !errors.Is(err, iofs.ErrPermission) {
		t.Fatalf("SECURITY VIOLATION: HostFS permitted reading symlink outside root: %v", err)
	}

	// 2. Attempt to stat escaping symlink
	_, err = hfs.Stat("/escape_link.txt")
	if !errors.Is(err, iofs.ErrPermission) {
		t.Fatalf("SECURITY VIOLATION: HostFS permitted stat on symlink outside root: %v", err)
	}

	// 3. Test writable HostFS with escaping directory creation
	hfsRW, err := fs.NewHostFS(mountRoot, fs.ReadWrite())
	if err != nil {
		t.Fatalf("failed to create HostFS RW: %v", err)
	}

	err = hfsRW.WriteFile("/escape_dir/new_file.txt", []byte("evil"), 0o644)
	if !errors.Is(err, iofs.ErrPermission) {
		t.Fatalf("SECURITY VIOLATION: HostFS permitted write through escaping dir symlink: %v", err)
	}
}

// Invariant 3: SSRF & Cloud Metadata Blocking
func TestAdversarial_SSRFAndCloudMetadata(t *testing.T) {
	pol := netpolicy.New(
		netpolicy.AllowHost("*", 80),
		netpolicy.AllowHost("*", 443),
	)

	blockedIPs := []string{
		"169.254.169.254",         // AWS / GCP / Azure metadata
		"fd00:ec2::254",           // AWS IPv6 metadata
		"127.0.0.1",               // IPv4 loopback
		"127.0.1.1",               // IPv4 loopback
		"::1",                     // IPv6 loopback
		"10.0.0.1",                // RFC 1918 Private Class A
		"172.16.0.1",              // RFC 1918 Private Class B
		"192.168.1.1",             // RFC 1918 Private Class C
		"169.254.1.1",             // Link-local
		"100.64.0.1",              // Carrier-Grade NAT (RFC 6598)
		"metadata.google.internal", // GCP metadata hostname
	}

	for _, target := range blockedIPs {
		ip := net.ParseIP(target)
		if ip != nil {
			err := pol.ValidateIP(ip)
			if !errors.Is(err, netpolicy.ErrPrivateIPBlocked) {
				t.Fatalf("SECURITY VIOLATION: SSRF blocker failed for IP %q: %v", target, err)
			}
		}

		// Also verify ValidateHostPort blocks metadata hosts
		if target == "metadata.google.internal" || target == "169.254.169.254" {
			err := pol.ValidateHostPort(target, 80)
			if err == nil {
				t.Fatalf("SECURITY VIOLATION: policy permitted metadata destination %q", target)
			}
		}
	}
}

// Invariant 4: Cross-Host Redirect Defense
func TestAdversarial_CrossHostRedirects(t *testing.T) {
	pipeServer, pipeClient := net.Pipe()
	defer pipeServer.Close()
	defer pipeClient.Close()

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Malicious redirect to cloud metadata
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		}),
	}
	go func() {
		_ = srv.Serve(&singleListener{conn: pipeServer})
	}()

	mockDialer := &pipeDialer{conn: pipeClient}
	mockResolver := &pipeResolver{
		ips: map[string][]net.IP{
			"api.allowed.com": {net.ParseIP("198.51.100.1")},
		},
	}

	pol := netpolicy.New(
		netpolicy.AllowHost("api.allowed.com", 80),
		netpolicy.WithDialer(mockDialer),
		netpolicy.WithResolver(mockResolver),
		netpolicy.WithAllowCrossHostRedirects(false), // strict default
	)

	client := pol.HTTPClient()
	req, _ := http.NewRequest("GET", "http://api.allowed.com/login", nil)
	_, err := client.Do(req)

	if err == nil || !strings.Contains(err.Error(), "cross-host redirect blocked") {
		t.Fatalf("SECURITY VIOLATION: cross-host redirect was not blocked: %v", err)
	}
}

// Invariant 5: Resource Exhaustion (Output & Command Budgets & Timeouts)
func TestAdversarial_ResourceExhaustion(t *testing.T) {
	ctx := context.Background()

	// 1. Infinite loop terminated by timeout
	sbTimeout, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		Limits: cordon.Limits{
			Timeout: 50 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	resTimeout, err := sbTimeout.ExecBash(ctx, "while true; do :; done")
	if err != nil {
		t.Fatalf("unexpected dispatch error on infinite loop: %v", err)
	}
	if !resTimeout.IsError || resTimeout.ExitCode != status.StatusTimeout {
		t.Fatalf("expected timeout exit code %d, got %d (stderr: %s)", status.StatusTimeout, resTimeout.ExitCode, resTimeout.Stderr)
	}

	// 2. Command flood terminated by MaxCommandCount
	sbFlood, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		Limits: cordon.Limits{
			MaxCommandCount: 5,
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	resFlood, err := sbFlood.ExecBash(ctx, "pwd && pwd && pwd && pwd && pwd && pwd && pwd")
	if err != nil {
		t.Fatalf("unexpected dispatch error on command flood: %v", err)
	}
	if !resFlood.IsError || !strings.Contains(resFlood.Stderr, "command limit exceeded") {
		t.Fatalf("expected command limit exceeded in stderr, got: %s", resFlood.Stderr)
	}

	// 3. Output flood terminated and capped
	sbOutput, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		Limits: cordon.Limits{
			MaxOutputBytes: 64,
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	resOutput, err := sbOutput.ExecBash(ctx, "for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do pwd; pwd; pwd; done")
	if err != nil {
		t.Fatalf("unexpected dispatch error on output flood: %v", err)
	}
	if !resOutput.IsError || !strings.Contains(resOutput.Stderr, "output byte limit exceeded") {
		t.Fatalf("expected output limit exceeded in stderr, got: %s", resOutput.Stderr)
	}
}

// Invariant 6: No Host Fallback
func TestAdversarial_NoHostFallback(t *testing.T) {
	// Provide only 'echo', disallow 'cat', 'ls', 'whoami', 'sh'
	echoCmd := commands.Core()[2] // 'pwd' or custom command
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(echoCmd),
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	ctx := context.Background()

	hostBinaries := []string{
		"whoami",
		"id",
		"/bin/sh -c 'echo pwn'",
		"/usr/bin/python3 -c 'print(1)'",
		"sudo ls",
	}

	for _, cmd := range hostBinaries {
		res, err := sb.ExecBash(ctx, cmd)
		if err != nil {
			t.Fatalf("unexpected dispatch error for %q: %v", cmd, err)
		}
		if res.ExitCode != status.StatusNotFound {
			t.Fatalf("SECURITY VIOLATION: command %q did not exit with 127 (StatusNotFound), got exit code %d", cmd, res.ExitCode)
		}
	}
}

// Invariant 7: Script Interpreter Confinement
func TestAdversarial_ScriptConfinement(t *testing.T) {
	sb, err := cordon.New(cordon.Policy{
		Limits: cordon.Limits{
			Timeout: 50 * time.Millisecond,
		},
		Tools: []cordon.ToolBinding{
			script.Python(),
			script.JS(),
		},
	})
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	ctx := context.Background()

	// Python cannot import or escape
	pyAttacks := []string{
		`import os; os.system("id")`,
		`import subprocess; subprocess.Popen(["ls"])`,
		`import socket; s = socket.socket()`,
		`load("os", "system")`,
	}
	for _, code := range pyAttacks {
		in, _ := json.Marshal(map[string]string{"code": code})
		res, _ := sb.CallTool(ctx, "python", in)
		if !res.IsError || res.ExitCode == 0 {
			t.Fatalf("SECURITY VIOLATION: python allowed forbidden operation: %q", code)
		}
	}

	// JS cannot escape or access process/require
	jsAttacks := []string{
		`require("child_process").execSync("id")`,
		`require("fs").readFileSync("/etc/passwd")`,
		`process.mainModule.require("child_process")`,
		`eval('require("net")')`,
	}
	for _, code := range jsAttacks {
		in, _ := json.Marshal(map[string]string{"code": code})
		res, _ := sb.CallTool(ctx, "js", in)
		if !res.IsError || res.ExitCode == 0 {
			t.Fatalf("SECURITY VIOLATION: js allowed forbidden operation: %q", code)
		}
	}
}

// Helpers
type singleListener struct {
	conn net.Conn
	once bool
}

func (l *singleListener) Accept() (net.Conn, error) {
	if l.once {
		return nil, net.ErrClosed
	}
	l.once = true
	return l.conn, nil
}

func (l *singleListener) Close() error   { return nil }
func (l *singleListener) Addr() net.Addr { return l.conn.LocalAddr() }

type pipeDialer struct {
	conn net.Conn
}

func (d *pipeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.conn, nil
}

type pipeResolver struct {
	ips map[string][]net.IP
}

func (r *pipeResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if ips, ok := r.ips[host]; ok {
		return ips, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host}
}
