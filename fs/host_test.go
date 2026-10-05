package fs_test

import (
	"errors"
	iofs "io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordon-dev/cordon/fs"
)

func TestHostFS_TraversalCannotEscape(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("inside root"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hfs, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	// 1. Legitimate file inside root
	data, err := hfs.ReadFile("/hello.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "inside root" {
		t.Errorf("got %q, want 'inside root'", string(data))
	}

	// 2. Traversal attempt: ../../hello.txt collapses to /hello.txt inside virtual root
	data, err = hfs.ReadFile("../../hello.txt")
	if err != nil {
		t.Fatalf("ReadFile with .. failed: %v", err)
	}
	if string(data) != "inside root" {
		t.Errorf("got %q, want 'inside root'", string(data))
	}

	// 3. Traversal attempt to read a file outside the mount root
	_, err = hfs.ReadFile("/../../etc/passwd")
	if err == nil {
		t.Errorf("expected error reading /../../etc/passwd, got nil")
	}
}

func TestHostFS_SymlinkOutsideRootRejected(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("super secret"), 0644); err != nil {
		t.Fatalf("failed to write outside file: %v", err)
	}

	// Create symlink pointing outside tempDir
	symlinkPath := filepath.Join(tempDir, "leak_link")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	hfs, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	// Accessing symlink that points outside the root must be rejected with ErrPermission
	_, err = hfs.ReadFile("/leak_link")
	if err == nil {
		t.Fatalf("expected permission error when reading escaping symlink, got nil")
	}
	if !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission for symlink escape, got %v", err)
	}

	_, err = hfs.Stat("/leak_link")
	if err == nil || !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission on Stat for symlink escape, got %v", err)
	}
}

func TestHostFS_ReadOnlyMountRejectsWrites(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to setup: %v", err)
	}

	hfs, err := fs.NewHostFS(tempDir, fs.ReadOnly())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	// 1. OpenFile for writing
	_, err = hfs.OpenFile("/new.txt", os.O_WRONLY|os.O_CREATE, 0644)
	if err == nil || !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission on OpenFile write, got %v", err)
	}

	// 2. WriteFile
	err = hfs.WriteFile("/new.txt", []byte("bad"), 0644)
	if err == nil || !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission on WriteFile, got %v", err)
	}

	// 3. Mkdir
	err = hfs.Mkdir("/sub", 0755)
	if err == nil || !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission on Mkdir, got %v", err)
	}

	// 4. Remove
	err = hfs.Remove("/file.txt")
	if err == nil || !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission on Remove, got %v", err)
	}

	// 5. Rename
	err = hfs.Rename("/file.txt", "/file2.txt")
	if err == nil || !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission on Rename, got %v", err)
	}
}

func TestHostFS_ReadWriteMount(t *testing.T) {
	tempDir := t.TempDir()

	hfs, err := fs.NewHostFS(tempDir, fs.ReadWrite())
	if err != nil {
		t.Fatalf("NewHostFS failed: %v", err)
	}

	// 1. Write file
	if err := hfs.WriteFile("/rw.txt", []byte("rw data"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 2. Verify on host
	hostContent, err := os.ReadFile(filepath.Join(tempDir, "rw.txt"))
	if err != nil || string(hostContent) != "rw data" {
		t.Fatalf("host content mismatch: %v, %q", err, string(hostContent))
	}

	// 3. Read back
	data, err := hfs.ReadFile("/rw.txt")
	if err != nil || string(data) != "rw data" {
		t.Fatalf("ReadFile mismatch: %v, %q", err, string(data))
	}

	// 4. Mkdir and Remove
	if err := hfs.Mkdir("/dir", 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	if err := hfs.Remove("/dir"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
}
