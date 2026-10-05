package fs_test

import (
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cordon-dev/cordon/fs"
)

// TestMemFS_IOFSReadChecks verifies io/fs.FS compatibility, including
// unrooted path handling, invalid path rejections, directory traversal with WalkDir,
// and reading files via standard library helpers.
func TestMemFS_IOFSReadChecks(t *testing.T) {
	mem := fs.NewMemFS(fs.WithFiles(map[string]string{
		"/hello.txt":        "hello world\n",
		"/data/config.json": `{"env":"production"}`,
		"/data/users.txt":   "alice\nbob\n",
		"/empty.txt":        "",
		"/nested/a/b/c.log": "log data",
	}))

	// 1. Valid unrooted Open
	f, err := mem.Open("hello.txt")
	if err != nil {
		t.Fatalf("Open('hello.txt') failed: %v", err)
	}
	content, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || string(content) != "hello world\n" {
		t.Fatalf("unexpected content from Open: %q, err=%v", string(content), err)
	}

	// 2. Open on "." returns root directory
	root, err := mem.Open(".")
	if err != nil {
		t.Fatalf("Open('.') failed: %v", err)
	}
	rootInfo, err := root.Stat()
	_ = root.Close()
	if err != nil || !rootInfo.IsDir() {
		t.Fatalf("expected root to be a directory, err=%v", err)
	}

	// 3. Open rejects invalid io/fs paths (rooted, .. traversal)
	if _, err := mem.Open("/hello.txt"); !errors.Is(err, iofs.ErrInvalid) {
		t.Errorf("expected ErrInvalid for rooted Open path, got %v", err)
	}
	if _, err := mem.Open("../escape"); !errors.Is(err, iofs.ErrInvalid) {
		t.Errorf("expected ErrInvalid for traversal Open path, got %v", err)
	}
	if _, err := mem.Open("data//config.json"); !errors.Is(err, iofs.ErrInvalid) {
		t.Errorf("expected ErrInvalid for double slash Open path, got %v", err)
	}

	// 4. Standard library fs.WalkDir compatibility
	var visited []string
	err = iofs.WalkDir(mem, ".", func(p string, d iofs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited = append(visited, p)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir failed: %v", err)
	}

	expectedVisited := []string{
		".",
		"data",
		"data/config.json",
		"data/users.txt",
		"empty.txt",
		"hello.txt",
		"nested",
		"nested/a",
		"nested/a/b",
		"nested/a/b/c.log",
	}

	if len(visited) != len(expectedVisited) {
		t.Fatalf("visited %v, want %v", visited, expectedVisited)
	}
	for i := range expectedVisited {
		if visited[i] != expectedVisited[i] {
			t.Errorf("visited[%d] = %q, want %q", i, visited[i], expectedVisited[i])
		}
	}

	// 5. Standard library fs.ReadFile compatibility
	data, err := iofs.ReadFile(mem, "data/config.json")
	if err != nil {
		t.Fatalf("iofs.ReadFile failed: %v", err)
	}
	if string(data) != `{"env":"production"}` {
		t.Errorf("got %q, want production json", string(data))
	}
}

func TestMemFS_ReadWritePersistence(t *testing.T) {
	mem := fs.NewMemFS()

	// 1. Initial write
	err := mem.WriteFile("/notes.txt", []byte("version 1"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, err := mem.ReadFile("/notes.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "version 1" {
		t.Errorf("got %q, want 'version 1'", string(data))
	}

	// 2. Overwrite
	err = mem.WriteFile("/notes.txt", []byte("version 2 updated"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile overwrite failed: %v", err)
	}
	data, err = mem.ReadFile("/notes.txt")
	if err != nil {
		t.Fatalf("ReadFile after overwrite failed: %v", err)
	}
	if string(data) != "version 2 updated" {
		t.Errorf("got %q, want 'version 2 updated'", string(data))
	}

	// 3. OpenFile append
	f, err := mem.OpenFile("/notes.txt", os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("OpenFile for append failed: %v", err)
	}
	_, err = f.Write([]byte(" - suffix"))
	if err != nil {
		t.Fatalf("f.Write failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("f.Close failed: %v", err)
	}

	data, err = mem.ReadFile("/notes.txt")
	if err != nil {
		t.Fatalf("ReadFile after append failed: %v", err)
	}
	if string(data) != "version 2 updated - suffix" {
		t.Errorf("got %q, want 'version 2 updated - suffix'", string(data))
	}

	// 4. Remove
	if err := mem.Remove("/notes.txt"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	_, err = mem.ReadFile("/notes.txt")
	if !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("expected ErrNotExist after remove, got %v", err)
	}
}

func TestMemFS_TraversalConfinement(t *testing.T) {
	mem := fs.NewMemFS()

	// Attempting traversal to create or write
	err := mem.WriteFile("../../escaped.txt", []byte("secret"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Should be saved at virtual root /escaped.txt
	data, err := mem.ReadFile("/escaped.txt")
	if err != nil {
		t.Fatalf("ReadFile /escaped.txt failed: %v", err)
	}
	if string(data) != "secret" {
		t.Errorf("got %q, want 'secret'", string(data))
	}

	// Reading with traversal sequences resolves inside virtual root
	data, err = mem.ReadFile("/a/b/../../escaped.txt")
	if err != nil {
		t.Fatalf("ReadFile traversal failed: %v", err)
	}
	if string(data) != "secret" {
		t.Errorf("got %q, want 'secret'", string(data))
	}
}

func TestVolume_RefuseRules(t *testing.T) {
	vol := fs.Mem().
		Seed(map[string]string{
			"/public/hello.txt":      "public content",
			"/etc/secrets/token":     "supersecret",
			"/private/id_rsa":        "key",
		}).
		Refuse("/etc/secrets/**", "*.key", "/private/**")

	// 1. Public file accessible
	data, err := vol.ReadFile("/public/hello.txt")
	if err != nil {
		t.Fatalf("failed reading public file: %v", err)
	}
	if string(data) != "public content" {
		t.Errorf("got %q, want 'public content'", string(data))
	}

	// 2. Refused paths return ErrPermission
	_, err = vol.ReadFile("/etc/secrets/token")
	if !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission for /etc/secrets/token, got %v", err)
	}

	_, err = vol.Stat("/private/id_rsa")
	if !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission for /private/id_rsa, got %v", err)
	}

	// 3. Subtree rule protection: removing ancestor /etc or /private is refused
	err = vol.RemoveAll("/private")
	if !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission for RemoveAll(/private), got %v", err)
	}

	err = vol.Rename("/private", "/moved_private")
	if !errors.Is(err, iofs.ErrPermission) {
		t.Errorf("expected ErrPermission for Rename(/private), got %v", err)
	}
}

func TestVolume_HideRules(t *testing.T) {
	vol := fs.Mem().
		Seed(map[string]string{
			"/visible.txt": "seen",
			"/secret.env":  "DB_PASSWORD=123",
			"/docs/readme": "docs",
		}).
		Hide("*.env", "/secret.env")

	// 1. Visible file is found
	data, err := vol.ReadFile("/visible.txt")
	if err != nil || string(data) != "seen" {
		t.Fatalf("unexpected error reading visible file: %v", err)
	}

	// 2. Hidden file returns ErrNotExist
	_, err = vol.ReadFile("/secret.env")
	if !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("expected ErrNotExist for hidden file, got %v", err)
	}

	_, err = vol.Stat("/secret.env")
	if !errors.Is(err, iofs.ErrNotExist) {
		t.Errorf("expected ErrNotExist on Stat for hidden file, got %v", err)
	}

	// 3. Directory listing omits hidden files
	entries, err := vol.ReadDir("/")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".env") {
			t.Errorf("directory listing leaked hidden file %q", e.Name())
		}
	}
}

func TestVolume_Limits(t *testing.T) {
	vol := fs.Mem().
		MaxFileBytes(20).
		MaxBytes(50)

	// 1. Single file byte limit
	err := vol.WriteFile("/small.txt", []byte("123456789012345"), 0o644) // 15 bytes
	if err != nil {
		t.Fatalf("WriteFile small failed: %v", err)
	}

	err = vol.WriteFile("/toolarge.txt", []byte("1234567890123456789012345"), 0o644) // 25 bytes > 20
	if !errors.Is(err, fs.ErrFileTooLarge) {
		t.Errorf("expected ErrFileTooLarge, got %v", err)
	}

	// 2. Total byte limit across multiple files
	// Currently used: 15 bytes. Total limit: 50.
	err = vol.WriteFile("/second.txt", []byte("12345678901234567890"), 0o644) // 20 bytes (total: 35)
	if err != nil {
		t.Fatalf("WriteFile second failed: %v", err)
	}

	// Attempting to add 20 bytes would reach 55 > 50 -> ErrNoSpace
	err = vol.WriteFile("/third.txt", []byte("12345678901234567890"), 0o644)
	if !errors.Is(err, fs.ErrNoSpace) {
		t.Errorf("expected ErrNoSpace when exceeding MaxBytes, got %v", err)
	}

	// Deleting /second.txt (20 bytes) drops total back to 15
	if err := vol.Remove("/second.txt"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Now third.txt (20 bytes) succeeds (total: 35 <= 50)
	err = vol.WriteFile("/third.txt", []byte("12345678901234567890"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile third after delete failed: %v", err)
	}
}

func TestMemFS_ConcurrentRace(t *testing.T) {
	mem := fs.NewMemFS(fs.WithFiles(map[string]string{
		"/shared.txt": "initial",
	}))

	const goroutines = 25
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			path := "/file_" + string(rune('a'+(id%26))) + ".txt"

			// Interleaved reads, writes, stats, and listings
			_ = mem.WriteFile(path, []byte("data"), 0o644)
			_, _ = mem.ReadFile(path)
			_, _ = mem.Stat(path)
			_, _ = mem.ReadDir("/")
			_, _ = mem.ReadFile("/shared.txt")
		}(i)
	}

	wg.Wait()
}
