package security_test

import (
	"os"
	"strings"
	"testing"
)

func TestDependencies_PureGoAndNoCgo(t *testing.T) {
	// 1. Verify go.mod exists and contains expected direct dependencies
	modBytes, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("failed to read go.mod: %v", err)
	}
	modStr := string(modBytes)

	// Ensure no known CGo or native bindings exist in dependencies
	forbiddenModules := []string{
		"github.com/mattn/go-sqlite3", // requires cgo
		"github.com/sbinet/go-python",  // requires cgo
		"C",                            // direct cgo
	}

	for _, forbidden := range forbiddenModules {
		if strings.Contains(modStr, forbidden) {
			t.Fatalf("SECURITY VIOLATION: forbidden CGo dependency found in go.mod: %s", forbidden)
		}
	}

	// 2. Verify all primary dependencies are pure-Go permissive modules
	expectedPureGoModules := []string{
		"github.com/goccy/sh",
		"github.com/dop251/goja",
		"go.starlark.net",
	}

	for _, mod := range expectedPureGoModules {
		if !strings.Contains(modStr, mod) {
			t.Errorf("expected module %q to be present in go.mod", mod)
		}
	}
}
