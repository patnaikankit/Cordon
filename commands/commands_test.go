package commands_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
)

func runCmd(t *testing.T, cmd command.Command, mem fs.FS, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	ec := &command.Context{
		Ctx:     context.Background(),
		Args:    append([]string{cmd.Name()}, args...),
		Dir:     "/",
		WorkDir: "/",
		Stdin:   strings.NewReader(stdin),
		Stdout:  &stdout,
		Stderr:  &stderr,
		FS:      mem,
	}

	err := cmd.Run(context.Background(), ec)
	code := 0
	if err != nil {
		if exitErr, ok := err.(*command.ExitError); ok {
			code = exitErr.Code
		} else {
			code = 1
		}
	}
	return stdout.String(), stderr.String(), code
}

func TestCat(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/a.txt": "alpha\n",
		"/b.txt": "beta\n",
	})

	// 1. Single file
	out, errOut, code := runCmd(t, commands.Cat, mem, "", "/a.txt")
	if code != 0 || out != "alpha\n" {
		t.Fatalf("cat failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 2. Concatenate multiple files
	out, _, code = runCmd(t, commands.Cat, mem, "", "/a.txt", "/b.txt")
	if code != 0 || out != "alpha\nbeta\n" {
		t.Fatalf("cat multi failed: code=%d out=%q", code, out)
	}

	// 3. Stdin via -
	out, _, code = runCmd(t, commands.Cat, mem, "piped\n", "-")
	if code != 0 || out != "piped\n" {
		t.Fatalf("cat - failed: code=%d out=%q", code, out)
	}

	// 4. Line numbering with -n
	out, _, code = runCmd(t, commands.Cat, mem, "", "-n", "/a.txt")
	if code != 0 || !strings.Contains(out, "1\talpha") {
		t.Fatalf("cat -n failed: code=%d out=%q", code, out)
	}

	// 5. Unsupported flag fails clearly
	_, errOut, code = runCmd(t, commands.Cat, mem, "", "-z", "/a.txt")
	if code != 2 || !strings.Contains(errOut, "invalid option") {
		t.Fatalf("cat -z expected code 2 and invalid option, got code=%d err=%q", code, errOut)
	}
}

func TestLs(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/dir/file1.txt": "content",
		"/dir/.hidden":   "secret",
		"/dir/sub/sub.txt": "deep",
	})

	// 1. Basic listing omits dotfiles
	out, _, code := runCmd(t, commands.Ls, mem, "", "/dir")
	if code != 0 || !strings.Contains(out, "file1.txt") || strings.Contains(out, ".hidden") {
		t.Fatalf("ls basic failed: code=%d out=%q", code, out)
	}

	// 2. -a includes dotfiles
	out, _, code = runCmd(t, commands.Ls, mem, "", "-a", "/dir")
	if code != 0 || !strings.Contains(out, ".hidden") {
		t.Fatalf("ls -a failed: code=%d out=%q", code, out)
	}

	// 3. -l long format
	out, _, code = runCmd(t, commands.Ls, mem, "", "-l", "/dir/file1.txt")
	if code != 0 || !strings.Contains(out, "file1.txt") || !strings.Contains(out, "7") {
		t.Fatalf("ls -l failed: code=%d out=%q", code, out)
	}

	// 4. Missing directory
	_, errOut, code := runCmd(t, commands.Ls, mem, "", "/nonexistent")
	if code != 2 || !strings.Contains(errOut, "cannot access") {
		t.Fatalf("ls nonexistent failed: code=%d err=%q", code, errOut)
	}

	// 5. Unsupported flag fails clearly
	_, errOut, code = runCmd(t, commands.Ls, mem, "", "-z")
	if code != 2 || !strings.Contains(errOut, "invalid option") {
		t.Fatalf("ls -z expected code 2, got code=%d err=%q", code, errOut)
	}
}

func TestPwd(t *testing.T) {
	mem := fs.Mem()
	out, _, code := runCmd(t, commands.Pwd, mem, "")
	if code != 0 || strings.TrimSpace(out) != "/" {
		t.Fatalf("pwd failed: code=%d out=%q", code, out)
	}
}

func TestMkdir(t *testing.T) {
	mem := fs.Mem()

	// 1. Create single dir
	_, _, code := runCmd(t, commands.Mkdir, mem, "", "/app")
	if code != 0 {
		t.Fatalf("mkdir /app failed: code=%d", code)
	}

	// 2. Nested dir without -p fails
	_, errOut, code := runCmd(t, commands.Mkdir, mem, "", "/a/b/c")
	if code != 1 || !strings.Contains(errOut, "cannot create directory") {
		t.Fatalf("mkdir /a/b/c without -p should fail: code=%d err=%q", code, errOut)
	}

	// 3. Nested dir with -p succeeds
	_, _, code = runCmd(t, commands.Mkdir, mem, "", "-p", "/a/b/c")
	if code != 0 {
		t.Fatalf("mkdir -p /a/b/c failed: code=%d", code)
	}
	info, err := mem.Stat("/a/b/c")
	if err != nil || !info.IsDir() {
		t.Fatalf("/a/b/c was not created")
	}

	// 4. Unsupported flag fails clearly
	_, errOut, code = runCmd(t, commands.Mkdir, mem, "", "-z", "/foo")
	if code != 2 || !strings.Contains(errOut, "invalid option") {
		t.Fatalf("mkdir -z expected code 2, got code=%d err=%q", code, errOut)
	}
}

func TestRm(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/file.txt":        "data",
		"/dir/subfile.txt": "sub",
	})

	// 1. Remove file
	_, _, code := runCmd(t, commands.Rm, mem, "", "/file.txt")
	if code != 0 {
		t.Fatalf("rm /file.txt failed: code=%d", code)
	}

	// 2. Removing directory without -r fails
	_, errOut, code := runCmd(t, commands.Rm, mem, "", "/dir")
	if code != 1 || !strings.Contains(errOut, "Is a directory") {
		t.Fatalf("rm dir without -r should fail: code=%d err=%q", code, errOut)
	}

	// 3. -r removes directory recursively
	_, _, code = runCmd(t, commands.Rm, mem, "", "-r", "/dir")
	if code != 0 {
		t.Fatalf("rm -r /dir failed: code=%d", code)
	}

	// 4. -f ignores missing files without error
	_, _, code = runCmd(t, commands.Rm, mem, "", "-f", "/already_gone.txt")
	if code != 0 {
		t.Fatalf("rm -f should succeed on missing file: code=%d", code)
	}
}

func TestCpAndMv(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/source.txt":      "hello cp",
		"/folder/base.txt": "folder",
	})

	// 1. cp file to new file
	_, _, code := runCmd(t, commands.Cp, mem, "", "/source.txt", "/copied.txt")
	if code != 0 {
		t.Fatalf("cp failed: code=%d", code)
	}
	data, _ := mem.ReadFile("/copied.txt")
	if string(data) != "hello cp" {
		t.Fatalf("copied content mismatch: %q", string(data))
	}

	// 2. cp file into existing directory
	_, _, code = runCmd(t, commands.Cp, mem, "", "/source.txt", "/folder")
	if code != 0 {
		t.Fatalf("cp to dir failed: code=%d", code)
	}
	data, _ = mem.ReadFile("/folder/source.txt")
	if string(data) != "hello cp" {
		t.Fatalf("copied into folder mismatch: %q", string(data))
	}

	// 3. mv file to new location
	_, _, code = runCmd(t, commands.Mv, mem, "", "/copied.txt", "/moved.txt")
	if code != 0 {
		t.Fatalf("mv failed: code=%d", code)
	}
	_, err := mem.ReadFile("/copied.txt")
	if err == nil {
		t.Fatalf("/copied.txt should no longer exist after mv")
	}
	data, _ = mem.ReadFile("/moved.txt")
	if string(data) != "hello cp" {
		t.Fatalf("moved file content mismatch: %q", string(data))
	}
}

func TestHeadAndTail(t *testing.T) {
	lines := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\n"
	mem := fs.Mem().Seed(map[string]string{
		"/doc.txt": lines,
	})

	// 1. head default 10 lines
	out, _, code := runCmd(t, commands.Head, mem, "", "/doc.txt")
	if code != 0 {
		t.Fatalf("head failed: code=%d", code)
	}
	if strings.Count(out, "\n") != 10 || !strings.Contains(out, "line10") || strings.Contains(out, "line11") {
		t.Fatalf("head default lines unexpected: %q", out)
	}

	// 2. head -n 3
	out, _, code = runCmd(t, commands.Head, mem, "", "-n", "3", "/doc.txt")
	if code != 0 || out != "line1\nline2\nline3\n" {
		t.Fatalf("head -n 3 failed: %q", out)
	}

	// 3. head -c 5 bytes
	out, _, code = runCmd(t, commands.Head, mem, "", "-c", "5", "/doc.txt")
	if code != 0 || out != "line1" {
		t.Fatalf("head -c 5 failed: %q", out)
	}

	// 4. tail -n 2
	out, _, code = runCmd(t, commands.Tail, mem, "", "-n", "2", "/doc.txt")
	if code != 0 || out != "line11\nline12\n" {
		t.Fatalf("tail -n 2 failed: %q", out)
	}

	// 5. tail -n +11 (start from line 11)
	out, _, code = runCmd(t, commands.Tail, mem, "", "-n", "+11", "/doc.txt")
	if code != 0 || out != "line11\nline12\n" {
		t.Fatalf("tail -n +11 failed: %q", out)
	}
}

func TestWc(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/sample.txt": "apple banana cherry\ndog elephant\n",
	})

	// Lines count -l
	out, _, code := runCmd(t, commands.Wc, mem, "", "-l", "/sample.txt")
	if code != 0 || !strings.Contains(out, "2") {
		t.Fatalf("wc -l failed: %q", out)
	}

	// Words count -w
	out, _, code = runCmd(t, commands.Wc, mem, "", "-w", "/sample.txt")
	if code != 0 || !strings.Contains(out, "5") {
		t.Fatalf("wc -w failed: %q", out)
	}

	// Default output (lines words bytes)
	out, _, code = runCmd(t, commands.Wc, mem, "", "/sample.txt")
	if code != 0 || !strings.Contains(out, "2 5") {
		t.Fatalf("wc default failed: %q", out)
	}
}

func TestGrep(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/log.txt": "INFO: startup\nERROR: timeout\nDEBUG: trace\nERROR: fatal failure\n",
	})

	// 1. Basic matching
	out, _, code := runCmd(t, commands.Grep, mem, "", "ERROR", "/log.txt")
	if code != 0 || !strings.Contains(out, "ERROR: timeout") || strings.Contains(out, "INFO") {
		t.Fatalf("grep basic failed: %q", out)
	}

	// 2. Invert match -v
	out, _, code = runCmd(t, commands.Grep, mem, "", "-v", "ERROR", "/log.txt")
	if code != 0 || strings.Contains(out, "ERROR") || !strings.Contains(out, "INFO: startup") {
		t.Fatalf("grep -v failed: %q", out)
	}

	// 3. Count only -c
	out, _, code = runCmd(t, commands.Grep, mem, "", "-c", "ERROR", "/log.txt")
	if code != 0 || strings.TrimSpace(out) != "2" {
		t.Fatalf("grep -c failed: %q", out)
	}

	// 4. Case insensitive -i
	out, _, code = runCmd(t, commands.Grep, mem, "", "-i", "error", "/log.txt")
	if code != 0 || !strings.Contains(out, "ERROR: timeout") {
		t.Fatalf("grep -i failed: %q", out)
	}

	// 5. Line numbers -n
	out, _, code = runCmd(t, commands.Grep, mem, "", "-n", "timeout", "/log.txt")
	if code != 0 || !strings.Contains(out, "2:ERROR: timeout") {
		t.Fatalf("grep -n failed: %q", out)
	}

	// 6. No match returns exit code 1
	out, _, code = runCmd(t, commands.Grep, mem, "", "NONEXISTENT_KEYWORD", "/log.txt")
	if code != 1 {
		t.Fatalf("grep no-match expected code 1, got %d", code)
	}
}

func TestSortAndUniq(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/items.txt": "banana\napple\ncherry\napple\ndate\n",
	})

	// 1. Basic sort
	out, _, code := runCmd(t, commands.Sort, mem, "", "/items.txt")
	if code != 0 {
		t.Fatalf("sort failed: code=%d", code)
	}
	expected := "apple\napple\nbanana\ncherry\ndate\n"
	if out != expected {
		t.Fatalf("sort got %q, want %q", out, expected)
	}

	// 2. Sort unique -u
	out, _, code = runCmd(t, commands.Sort, mem, "", "-u", "/items.txt")
	if code != 0 {
		t.Fatalf("sort -u failed: code=%d", code)
	}
	expectedUniq := "apple\nbanana\ncherry\ndate\n"
	if out != expectedUniq {
		t.Fatalf("sort -u got %q, want %q", out, expectedUniq)
	}

	// 3. Reverse sort -r
	out, _, code = runCmd(t, commands.Sort, mem, "", "-r", "/items.txt")
	if code != 0 || !strings.HasPrefix(out, "date\ncherry\n") {
		t.Fatalf("sort -r failed: %q", out)
	}

	// 4. uniq on sorted input
	out, _, code = runCmd(t, commands.Uniq, mem, expected)
	if code != 0 || out != expectedUniq {
		t.Fatalf("uniq failed: got %q, want %q", out, expectedUniq)
	}

	// 5. uniq -c (counts)
	out, _, code = runCmd(t, commands.Uniq, mem, expected, "-c")
	if code != 0 || !strings.Contains(out, "2 apple") {
		t.Fatalf("uniq -c failed: %q", out)
	}
}

func TestCutAndTr(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/data.csv": "alice,30,engineer\nbob,25,designer\n",
	})

	// 1. cut -d , -f 1,3
	out, _, code := runCmd(t, commands.Cut, mem, "", "-d", ",", "-f", "1,3", "/data.csv")
	if code != 0 {
		t.Fatalf("cut failed: code=%d", code)
	}
	expected := "alice,engineer\nbob,designer\n"
	if out != expected {
		t.Fatalf("cut got %q, want %q", out, expected)
	}

	// 2. cut -c 1-4 (character range)
	out, _, code = runCmd(t, commands.Cut, mem, "", "-c", "1-4", "/data.csv")
	if code != 0 || out != "alic\nbob,\n" {
		t.Fatalf("cut -c failed: %q", out)
	}

	// 3. tr translation a-z to A-Z
	out, _, code = runCmd(t, commands.Tr, mem, "hello world", "a-z", "A-Z")
	if code != 0 || out != "HELLO WORLD" {
		t.Fatalf("tr translation failed: %q", out)
	}

	// 4. tr delete -d
	out, _, code = runCmd(t, commands.Tr, mem, "user123_test456", "-d", "0-9")
	if code != 0 || out != "user_test" {
		t.Fatalf("tr -d failed: %q", out)
	}

	// 5. tr squeeze -s
	out, _, code = runCmd(t, commands.Tr, mem, "too   many    spaces", "-s", " ")
	if code != 0 || out != "too many spaces" {
		t.Fatalf("tr -s failed: %q", out)
	}
}

func TestBase64(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/hello.txt": "Hello, Cordon!\n",
	})

	// 1. Encode from stdin
	out, _, code := runCmd(t, commands.Base64, mem, "Cordon Sandbox")
	if code != 0 || strings.TrimSpace(out) != "Q29yZG9uIFNhbmRib3g=" {
		t.Fatalf("base64 encode stdin failed: code=%d, out=%q", code, out)
	}

	// 2. Decode from stdin (-d)
	out, _, code = runCmd(t, commands.Base64, mem, "Q29yZG9uIFNhbmRib3g=", "-d")
	if code != 0 || out != "Cordon Sandbox" {
		t.Fatalf("base64 decode stdin failed: code=%d, out=%q", code, out)
	}

	// 3. Encode file
	out, _, code = runCmd(t, commands.Base64, mem, "", "/hello.txt")
	if code != 0 || strings.TrimSpace(out) != "SGVsbG8sIENvcmRvbiEK" {
		t.Fatalf("base64 encode file failed: %q", out)
	}

	// 4. Invalid base64 decode input
	_, stderr, code := runCmd(t, commands.Base64, mem, "not-valid-base64!!!", "-d")
	if code != 1 || !strings.Contains(stderr, "invalid") {
		t.Fatalf("expected error on invalid base64, got code=%d, stderr=%q", code, stderr)
	}
}

func TestSha256sum(t *testing.T) {
	mem := fs.Mem().Seed(map[string]string{
		"/msg.txt": "hello\n",
	})

	// 1. Stdin
	out, _, code := runCmd(t, commands.Sha256sum, mem, "hello\n")
	if code != 0 {
		t.Fatalf("sha256sum stdin failed: code=%d", code)
	}
	expectedStdinHash := "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03  -\n"
	if out != expectedStdinHash {
		t.Fatalf("sha256sum got %q, want %q", out, expectedStdinHash)
	}

	// 2. File
	out, _, code = runCmd(t, commands.Sha256sum, mem, "", "/msg.txt")
	if code != 0 {
		t.Fatalf("sha256sum file failed: code=%d", code)
	}
	expectedFileHash := "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03  /msg.txt\n"
	if out != expectedFileHash {
		t.Fatalf("sha256sum file got %q, want %q", out, expectedFileHash)
	}
}

func TestAllAndCoreRegistration(t *testing.T) {
	allCmds := commands.All()
	if len(allCmds) != 17 {
		t.Fatalf("expected 17 core commands in v1, got %d", len(allCmds))
	}

	expectedNames := map[string]bool{
		"cat": true, "ls": true, "pwd": true, "mkdir": true, "rm": true, "cp": true, "mv": true,
		"head": true, "tail": true, "wc": true, "grep": true, "sort": true, "uniq": true, "cut": true, "tr": true,
		"base64": true, "sha256sum": true,
	}

	for _, c := range allCmds {
		if !expectedNames[c.Name()] {
			t.Errorf("unexpected command name %q in Core()", c.Name())
		}
	}
}
