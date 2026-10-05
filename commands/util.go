package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"path"
	"strings"

	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
)

// errMsg formats filesystem errors consistently with standard coreutils phrasing.
func errMsg(err error) string {
	switch {
	case errors.Is(err, iofs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, iofs.ErrExist):
		return "File exists"
	case errors.Is(err, iofs.ErrPermission):
		return "Permission denied"
	}
	var pe *iofs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// stringLines splits data into lines, omitting an empty element for a trailing newline.
func stringLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// gatherInput reads all operand files (or stdin when files is empty) into a single buffer.
// A "-" operand reads stdin.
func gatherInput(ec *command.Context, cmd string, files []string) ([]byte, int) {
	if len(files) == 0 {
		data, err := readAll(ec)
		if err != nil {
			ec.Errorf("%s: %v\n", cmd, err)
			return nil, 1
		}
		return data, 0
	}
	var buf []byte
	exit := 0
	for _, name := range files {
		var (
			data []byte
			err  error
		)
		if name == "-" {
			data, err = readAll(ec)
		} else {
			data, err = ec.FS.ReadFile(ec.Resolve(name))
		}
		if err != nil {
			ec.Errorf("%s: %s: %s\n", cmd, name, errMsg(err))
			exit = 1
			continue
		}
		buf = append(buf, data...)
	}
	return buf, exit
}

func readAll(ec *command.Context) ([]byte, error) {
	if ec.Stdin == nil {
		return nil, nil
	}
	return io.ReadAll(ec.Stdin)
}

const maxLineBytes = 16 << 20

func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	return sc
}

func joinDisp(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}

type move struct {
	srcDisp, srcAbs string
	dstDisp         string
	targetAbs       string
}

func resolveMoves(ec *command.Context, cmd string, args []string) ([]move, error) {
	if len(args) < 2 {
		return nil, ec.Fail(1, "%s: missing file operand\n", cmd)
	}
	dst := args[len(args)-1]
	srcs := args[:len(args)-1]
	dstAbs := ec.Resolve(dst)
	dstInfo, dstErr := ec.FS.Stat(dstAbs)
	dstIsDir := dstErr == nil && dstInfo.IsDir()
	if len(srcs) > 1 && !dstIsDir {
		return nil, ec.Fail(1, "%s: target '%s' is not a directory\n", cmd, dst)
	}
	moves := make([]move, 0, len(srcs))
	for _, src := range srcs {
		srcAbs := ec.Resolve(src)
		target := dstAbs
		if dstIsDir {
			target = fs.Clean(path.Join(dstAbs, path.Base(srcAbs)))
		}
		moves = append(moves, move{
			srcDisp:   src,
			srcAbs:    srcAbs,
			dstDisp:   dst,
			targetAbs: target,
		})
	}
	return moves, nil
}

func copyPath(ec *command.Context, src, dst string, recursive bool) error {
	info, err := ec.FS.Stat(src)
	if err != nil {
		return fmt.Errorf("cannot stat '%s': %s", src, errMsg(err))
	}
	if info.IsDir() {
		return copyDir(ec, src, dst, recursive)
	}
	data, err := ec.FS.ReadFile(src)
	if err != nil {
		return err
	}
	return ec.FS.WriteFile(dst, data, info.Mode().Perm())
}

func copyDir(ec *command.Context, src, dst string, recursive bool) error {
	if !recursive {
		return fmt.Errorf("-r not specified; omitting directory '%s'", src)
	}
	if isSelfOrDescendant(dst, src) {
		return fmt.Errorf("cannot copy a directory, '%s', into itself, '%s'", src, dst)
	}
	if err := ec.FS.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := ec.FS.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := path.Join(src, e.Name())
		d := path.Join(dst, e.Name())
		if err := copyPath(ec, s, d, recursive); err != nil {
			return err
		}
	}
	return nil
}

func isSelfOrDescendant(p, dir string) bool {
	p, dir = fs.Clean(p), fs.Clean(dir)
	return p == dir || strings.HasPrefix(p, dir+"/")
}
