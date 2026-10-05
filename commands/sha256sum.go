package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/cordon-dev/cordon/command"
)

type sha256sumCmd struct{}

func (sha256sumCmd) Name() string { return "sha256sum" }

func (sha256sumCmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "", "")
	if err != nil {
		return ec.Fail(2, "sha256sum: %v\n", err)
	}

	hashReader := func(r io.Reader, label string) error {
		h := sha256.New()
		buf := make([]byte, 32*1024)
		for {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			n, err := r.Read(buf)
			if n > 0 {
				h.Write(buf[:n])
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
		}
		digest := hex.EncodeToString(h.Sum(nil))
		fmt.Fprintf(ec.Stdout, "%s  %s\n", digest, label)
		return nil
	}

	if len(o.args) == 0 {
		if err := hashReader(ec.StdinReader(), "-"); err != nil {
			return ec.Fail(1, "sha256sum: %v\n", err)
		}
		return nil
	}

	exit := 0
	for _, name := range o.args {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if name == "-" {
			if err := hashReader(ec.StdinReader(), "-"); err != nil {
				ec.Errorf("sha256sum: %v\n", err)
				exit = 1
			}
			continue
		}
		data, err := ec.FS.ReadFile(ec.Resolve(name))
		if err != nil {
			ec.Errorf("sha256sum: %s: %s\n", name, errMsg(err))
			exit = 1
			continue
		}
		if err := hashReader(bytes.NewReader(data), name); err != nil {
			ec.Errorf("sha256sum: %v\n", err)
			exit = 1
		}
	}
	return command.Exit(exit)
}

// Sha256sum is the built-in SHA-256 digest computation command.
var Sha256sum command.Command = sha256sumCmd{}

