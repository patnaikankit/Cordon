package fs_test

import (
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordon-dev/cordon/fs"
)

func TestOverlayFS_ReadsFallbackToLower(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "lower.txt"), []byte("lower data"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	hostFS, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	cow := fs.CoW(hostFS)

	// 1. Read lower file through CoW overlay
	data, err := cow.ReadFile("/lower.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "lower data" {
		t.Errorf("got %q, want 'lower data'", string(data))
	}

	// 2. Stat lower file through CoW overlay
	info, err := cow.Stat("/lower.txt")
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size() != int64(len("lower data")) {
		t.Errorf("expected size %d, got %d", len("lower data"), info.Size())
	}
}

func TestOverlayFS_WritesNeverModifyLowerHost(t *testing.T) {
	tempDir := t.TempDir()
	origFile := filepath.Join(tempDir, "base.txt")
	if err := os.WriteFile(origFile, []byte("original host content"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	hostFS, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	cow := fs.CoW(hostFS)

	// 1. Overwrite existing lower file in CoW overlay
	if err := cow.WriteFile("/base.txt", []byte("mutated in memory"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 2. Verify overlay reads mutated data
	cowData, err := cow.ReadFile("/base.txt")
	if err != nil || string(cowData) != "mutated in memory" {
		t.Fatalf("cow read got %q, want 'mutated in memory'", string(cowData))
	}

	// 3. Verify underlying host file is 100% unchanged!
	hostData, err := os.ReadFile(origFile)
	if err != nil || string(hostData) != "original host content" {
		t.Fatalf("HOST FILE MODIFIED! Got %q, want 'original host content'", string(hostData))
	}

	// 4. Create brand new file in CoW
	if err := cow.WriteFile("/new_mem.txt", []byte("in memory only"), 0644); err != nil {
		t.Fatalf("WriteFile new failed: %v", err)
	}
	// Verify host directory does not contain new_mem.txt
	if _, err := os.Stat(filepath.Join(tempDir, "new_mem.txt")); !os.IsNotExist(err) {
		t.Fatalf("new file leaked to host disk!")
	}
}

func TestOverlayFS_WhiteoutDeletion(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "item1.txt")
	f2 := filepath.Join(tempDir, "item2.txt")
	if err := os.WriteFile(f1, []byte("item1"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(f2, []byte("item2"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	hostFS, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	cow := fs.CoW(hostFS)

	// 1. Verify ReadDir before deletion lists both items
	entries, err := cow.ReadDir("/")
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d (err=%v)", len(entries), err)
	}

	// 2. Delete item1 through CoW overlay
	if err := cow.Remove("/item1.txt"); err != nil {
		t.Fatalf("Remove /item1.txt failed: %v", err)
	}

	// 3. Stat on deleted item should report ErrNotExist
	_, err = cow.Stat("/item1.txt")
	if err == nil || !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("expected ErrNotExist on whited out file, got %v", err)
	}

	// 4. ReadFile on deleted item should report ErrNotExist
	_, err = cow.ReadFile("/item1.txt")
	if err == nil || !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("expected ErrNotExist on ReadFile for whited out file, got %v", err)
	}

	// 5. ReadDir should now only list item2.txt
	entries, err = cow.ReadDir("/")
	if err != nil || len(entries) != 1 || entries[0].Name() != "item2.txt" {
		t.Fatalf("expected [item2.txt], got %+v", entries)
	}

	// 6. Underlying host file item1.txt must still exist!
	if _, err := os.Stat(f1); err != nil {
		t.Fatalf("host file was deleted! %v", err)
	}

	// 7. Re-creating item1.txt clears the whiteout
	if err := cow.WriteFile("/item1.txt", []byte("recreated"), 0644); err != nil {
		t.Fatalf("WriteFile recreate failed: %v", err)
	}
	recreatedData, err := cow.ReadFile("/item1.txt")
	if err != nil || string(recreatedData) != "recreated" {
		t.Errorf("recreated read failed: %q", string(recreatedData))
	}
}

func TestOverlayFS_RenameWithWhiteout(t *testing.T) {
	tempDir := t.TempDir()
	origFile := filepath.Join(tempDir, "doc.txt")
	if err := os.WriteFile(origFile, []byte("text data"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	hostFS, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	cow := fs.CoW(hostFS)

	// Rename lower file
	if err := cow.Rename("/doc.txt", "/renamed.txt"); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}

	// Old name is whited out
	_, err = cow.Stat("/doc.txt")
	if err == nil || !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("expected ErrNotExist for old path, got %v", err)
	}

	// New name contains original data
	data, err := cow.ReadFile("/renamed.txt")
	if err != nil || string(data) != "text data" {
		t.Errorf("new path read mismatch: %v, %q", err, string(data))
	}

	// Host file doc.txt remains untouched
	hostData, err := os.ReadFile(origFile)
	if err != nil || string(hostData) != "text data" {
		t.Errorf("host file corrupted: %v, %q", err, string(hostData))
	}
}

func TestOverlayFS_OpenFileAppend(t *testing.T) {
	tempDir := t.TempDir()
	origFile := filepath.Join(tempDir, "append.txt")
	if err := os.WriteFile(origFile, []byte("header\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	hostFS, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	cow := fs.CoW(hostFS)

	// Open for append in CoW
	f, err := cow.OpenFile("/append.txt", os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("OpenFile append failed: %v", err)
	}
	_, _ = io.WriteString(f, "line 2\n")
	_ = f.Close()

	// Verify overlay has both lines
	content, err := cow.ReadFile("/append.txt")
	if err != nil || string(content) != "header\nline 2\n" {
		t.Fatalf("overlay content got %q", string(content))
	}

	// Verify host file has only the original line
	hostContent, _ := os.ReadFile(origFile)
	if string(hostContent) != "header\n" {
		t.Fatalf("host content was modified! %q", string(hostContent))
	}
}
