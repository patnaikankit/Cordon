package fs

import (
	"path"
	"strings"
)

// Clean returns name as a cleaned, rooted, slash-separated path.
// A leading "/" is prepended when missing, and any ".." that would climb above the root
// is collapsed by path.Clean to "/". Therefore, any path processed by Clean is guaranteed
// to remain strictly inside the virtual filesystem namespace.
// This is the single choke point providing path-traversal confinement.
func Clean(name string) string {
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return path.Clean(name)
}

// Resolve interprets name relative to dir (both within the virtual filesystem namespace)
// and returns a cleaned, rooted path. Absolute names ignore dir.
func Resolve(dir, name string) string {
	if strings.HasPrefix(name, "/") {
		return Clean(name)
	}
	if dir == "" {
		dir = "/"
	}
	return Clean(path.Join(dir, name))
}
