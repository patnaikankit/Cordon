package fuzz_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/netpolicy"
)

func FuzzPathClean(f *testing.F) {
	seeds := []string{
		"/",
		"",
		".",
		"..",
		"/a/b/c",
		"/a/../b",
		"../../../etc/passwd",
		"/foo//bar///baz",
		"/app/././data",
		"/app/data\x00/../../etc",
		"//..//..//",
		"a/b/c/../../../../../../root",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		cleaned := fs.Clean(input)

		// Invariant 1: Cleaned path must always start with "/"
		if !strings.HasPrefix(cleaned, "/") {
			t.Fatalf("Clean(%q) = %q does not start with /", input, cleaned)
		}

		// Invariant 2: Must never contain "/../" or "/./" or "//"
		if strings.Contains(cleaned, "/../") || strings.Contains(cleaned, "/./") || strings.Contains(cleaned, "//") {
			t.Fatalf("Clean(%q) = %q contains un-normalized components", input, cleaned)
		}

		// Invariant 3: Idempotence: Clean(Clean(p)) == Clean(p)
		if fs.Clean(cleaned) != cleaned {
			t.Fatalf("Clean is not idempotent for %q: Clean(%q) = %q", input, cleaned, fs.Clean(cleaned))
		}
	})
}

func FuzzHostMatch(f *testing.F) {
	seeds := []string{
		"api.example.com",
		"*.example.com",
		"*",
		"127.0.0.1",
		"169.254.169.254",
		"example.com:8080",
		"http://evil.com",
		"localhost",
		"::1",
		"test..com",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, host string) {
		pol := netpolicy.New(
			netpolicy.AllowHost("api.example.com", 443),
			netpolicy.AllowHost("*.internal.net", 80),
			netpolicy.DenyRule(netpolicy.Rule{Host: "evil.internal.net"}),
		)

		// Ensure policy validation does not panic on any malformed input
		_ = pol.ValidateHostPort(host, 80)
		if u, err := url.Parse("http://" + host); err == nil {
			_ = pol.ValidateURL(u, "GET")
		}
	})
}

func FuzzShellInput(f *testing.F) {
	seeds := []string{
		"echo hello",
		"cat /dev/null",
		"ls -la",
		"echo $VAR",
		"$(whoami)",
		"`id`",
		"echo foo | grep bar",
		"if true; then echo ok; fi",
		"for i in 1 2 3; do echo $i; done",
		"../../../etc/passwd",
		"rm -rf /",
		";;; &&& |||",
		"((a=1))",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       fs.Mem(),
		Limits: cordon.Limits{
			Timeout:         50 * time.Millisecond,
			MaxOutputBytes:  256,
			MaxCommandCount: 10,
		},
	})
	if err != nil {
		f.Fatalf("failed to create sandbox: %v", err)
	}

	f.Fuzz(func(t *testing.T, input string) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Shell execution must never panic and must strictly observe budgets
		res, err := sb.ExecBash(ctx, input)
		if err != nil {
			// Dispatch error is acceptable for malformed input
			return
		}

		// MaxOutputBytes invariant check
		if int64(len(res.Stdout)+len(res.Stderr)) > 512 { // allowing truncation overhead message
			t.Fatalf("output exceeded budget: stdout=%d, stderr=%d", len(res.Stdout), len(res.Stderr))
		}
	})
}
