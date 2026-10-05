package fs_test

import (
	"testing"

	"github.com/cordon-dev/cordon/fs"
)

func TestPathClean(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "/"},
		{".", "/"},
		{"/", "/"},
		{"a/b/c", "/a/b/c"},
		{"/a/b/c", "/a/b/c"},
		{"../../etc/passwd", "/etc/passwd"},
		{"/a/b/../../c", "/c"},
		{"/a/b/../../../..", "/"},
		{"a/./b/../c", "/a/c"},
		{"///a///b///", "/a/b"},
	}

	for _, tc := range tests {
		got := fs.Clean(tc.input)
		if got != tc.expected {
			t.Errorf("Clean(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestPathResolve(t *testing.T) {
	tests := []struct {
		dir      string
		name     string
		expected string
	}{
		{"/app", "config.json", "/app/config.json"},
		{"/app", "../secret.txt", "/secret.txt"},
		{"/app", "/absolute/path", "/absolute/path"},
		{"", "foo.txt", "/foo.txt"},
		{"/", "bar.txt", "/bar.txt"},
		{"/a/b", "../../c/d", "/c/d"},
	}

	for _, tc := range tests {
		got := fs.Resolve(tc.dir, tc.name)
		if got != tc.expected {
			t.Errorf("Resolve(%q, %q) = %q, want %q", tc.dir, tc.name, got, tc.expected)
		}
	}
}
