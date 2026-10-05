package fs

import (
	iofs "io/fs"
	"os"
	"path"
	"strings"
)

// Op indicates the category of filesystem operation for access-control rules.
type Op int

const (
	OpRead Op = iota
	OpWrite
	OpList
	OpStat
)

func (o Op) String() string {
	switch o {
	case OpRead:
		return "open"
	case OpWrite:
		return "write"
	case OpList:
		return "readdir"
	case OpStat:
		return "stat"
	default:
		return "open"
	}
}

type ruleKind int

const (
	ruleRefuse ruleKind = iota // Deny with permission error
	ruleHide                   // Deny with not-exist error (and omitted from listings)
)

type rule struct {
	kind  ruleKind
	globs []string
}

// Volume wraps a base filesystem with policy enforcement, access rules,
// and limit configurations. It implements FS and can be passed anywhere an FS is expected.
type Volume struct {
	base   FS
	mem    *MemFS // non-nil when backed by MemFS (enables Seed and byte limits)
	rules  []rule
	access func(op Op, path string) error
}

// Nop returns an empty in-memory filesystem with default policy (no files).
func Nop() *Volume {
	m := NewMemFS()
	return &Volume{base: m, mem: m}
}

// Mem returns a fresh in-memory filesystem ready to be seeded or customized.
func Mem() *Volume {
	return Nop()
}

func (v *Volume) requireMem(method string) {
	if v.mem == nil {
		panic("fs: " + method + " is only supported on an in-memory volume (Mem/Nop)")
	}
}

// Seed initializes the in-memory volume with files from a path -> content map.
func (v *Volume) Seed(files map[string]string) *Volume {
	v.requireMem("Seed")
	for name, content := range files {
		p := Clean(name)
		_ = v.mem.MkdirAll(path.Dir(p), 0o755)
		_ = v.mem.WriteFile(p, []byte(content), 0o644)
	}
	return v
}

// MaxBytes caps the total storage bytes for the volume.
func (v *Volume) MaxBytes(total int64) *Volume {
	v.requireMem("MaxBytes")
	v.mem.limits.MaxTotalBytes = total
	return v
}

// MaxFileBytes caps the maximum size of any single file.
func (v *Volume) MaxFileBytes(n int64) *Volume {
	v.requireMem("MaxFileBytes")
	v.mem.limits.MaxFileBytes = n
	return v
}

// Refuse denies access to any path matching the given globs with a permission error.
// A trailing "/**" matches the directory and all of its descendants.
func (v *Volume) Refuse(globs ...string) *Volume {
	v.rules = append(v.rules, rule{kind: ruleRefuse, globs: globs})
	return v
}

// Hide causes paths matching any of the given globs to appear nonexistent
// (returning not-exist error on open/stat and omitting them from directory listings).
func (v *Volume) Hide(globs ...string) *Volume {
	v.rules = append(v.rules, rule{kind: ruleHide, globs: globs})
	return v
}

// Access installs a custom access verification callback.
func (v *Volume) Access(fn func(op Op, path string) error) *Volume {
	v.access = fn
	return v
}

func (v *Volume) ruleRels(name string) []string {
	rel := strings.TrimPrefix(Clean(name), "/")
	return []string{rel}
}

// matchAnyGlob checks if a relative path matches any glob.
func matchAnyGlob(globs []string, rel string) bool {
	base := path.Base(rel)
	for _, g := range globs {
		g = strings.TrimPrefix(g, "/")
		if prefix, ok := strings.CutSuffix(g, "/**"); ok {
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(g, rel); ok {
			return true
		}
		if ok, _ := path.Match(g, base); ok {
			return true
		}
	}
	return false
}

// matchAnyRel reports whether any candidate path matches any glob.
func matchAnyRel(globs, rels []string) bool {
	for _, rel := range rels {
		if matchAnyGlob(globs, rel) {
			return true
		}
	}
	return false
}

// check evaluates access rules for the given operation on path name.
func (v *Volume) check(op Op, name string) error {
	if len(v.rules) == 0 && v.access == nil {
		return nil
	}
	rels := v.ruleRels(name)
	for _, r := range v.rules {
		if !matchAnyRel(r.globs, rels) {
			continue
		}
		if r.kind == ruleHide {
			return pathErr(op.String(), name, iofs.ErrNotExist)
		}
		return pathErr(op.String(), name, iofs.ErrPermission)
	}
	if v.access != nil {
		p := Clean(name)
		if err := v.access(op, p); err != nil {
			return err
		}
	}
	return nil
}

// hasHideRule reports whether any Hide rule is active.
func (v *Volume) hasHideRule() bool {
	for _, r := range v.rules {
		if r.kind == ruleHide {
			return true
		}
	}
	return false
}

// hidden reports whether rel matches any Hide rule.
func (v *Volume) hidden(rel string) bool {
	rels := []string{strings.TrimPrefix(Clean("/"+rel), "/")}
	for _, r := range v.rules {
		if r.kind == ruleHide && matchAnyRel(r.globs, rels) {
			return true
		}
	}
	return false
}

// subtreeRuled reports whether name or any of its descendants matches any rule.
func (v *Volume) subtreeRuled(name string) bool {
	if len(v.rules) == 0 {
		return false
	}
	prefix := strings.TrimPrefix(Clean(name), "/")
	if prefix == "" {
		return len(v.rules) > 0
	}
	for _, r := range v.rules {
		for _, g := range r.globs {
			cg := strings.TrimPrefix(g, "/")
			if cg == prefix || strings.HasPrefix(cg, prefix+"/") {
				return true
			}
			if matchAnyGlob([]string{g}, prefix) {
				return true
			}
		}
	}
	return false
}

// FS implementation methods

func (v *Volume) Open(name string) (iofs.File, error) {
	if err := v.check(OpRead, name); err != nil {
		return nil, err
	}
	return v.base.Open(name)
}

func (v *Volume) OpenFile(name string, flag int, perm iofs.FileMode) (File, error) {
	op := OpRead
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		op = OpWrite
	}
	if err := v.check(op, name); err != nil {
		return nil, err
	}
	return v.base.OpenFile(name, flag, perm)
}

func (v *Volume) Stat(name string) (iofs.FileInfo, error) {
	if err := v.check(OpStat, name); err != nil {
		return nil, err
	}
	return v.base.Stat(name)
}

func (v *Volume) ReadDir(name string) ([]iofs.DirEntry, error) {
	if err := v.check(OpList, name); err != nil {
		return nil, err
	}
	entries, err := v.base.ReadDir(name)
	if err != nil {
		return nil, err
	}
	if !v.hasHideRule() {
		return entries, nil
	}
	dir := strings.TrimPrefix(Clean(name), "/")
	kept := make([]iofs.DirEntry, 0, len(entries))
	for _, e := range entries {
		rel := e.Name()
		if dir != "" {
			rel = dir + "/" + e.Name()
		}
		if v.hidden(rel) {
			continue
		}
		kept = append(kept, e)
	}
	return kept, nil
}

func (v *Volume) ReadFile(name string) ([]byte, error) {
	if err := v.check(OpRead, name); err != nil {
		return nil, err
	}
	return v.base.ReadFile(name)
}

func (v *Volume) WriteFile(name string, data []byte, perm iofs.FileMode) error {
	if err := v.check(OpWrite, name); err != nil {
		return err
	}
	return v.base.WriteFile(name, data, perm)
}

func (v *Volume) Mkdir(name string, perm iofs.FileMode) error {
	if err := v.check(OpWrite, name); err != nil {
		return err
	}
	return v.base.Mkdir(name, perm)
}

func (v *Volume) MkdirAll(name string, perm iofs.FileMode) error {
	if err := v.check(OpWrite, name); err != nil {
		return err
	}
	return v.base.MkdirAll(name, perm)
}

func (v *Volume) Remove(name string) error {
	if err := v.check(OpWrite, name); err != nil {
		return err
	}
	return v.base.Remove(name)
}

func (v *Volume) RemoveAll(name string) error {
	if err := v.check(OpWrite, name); err != nil {
		return err
	}
	if v.subtreeRuled(name) {
		return pathErr("remove", name, iofs.ErrPermission)
	}
	return v.base.RemoveAll(name)
}

func (v *Volume) Rename(oldpath, newpath string) error {
	if err := v.check(OpWrite, oldpath); err != nil {
		return err
	}
	if err := v.check(OpWrite, newpath); err != nil {
		return err
	}
	if v.subtreeRuled(oldpath) {
		return pathErr("rename", oldpath, iofs.ErrPermission)
	}
	return v.base.Rename(oldpath, newpath)
}

var _ FS = (*Volume)(nil)
