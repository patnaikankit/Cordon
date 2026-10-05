package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/cordon-dev/cordon/command"
)

type base64Cmd struct{}

func (base64Cmd) Name() string { return "base64" }

func (base64Cmd) Run(ctx context.Context, ec *command.Context) error {
	o, err := parseOpts(ec.Args[1:], "d", "")
	if err != nil {
		return ec.Fail(2, "base64: %v\n", err)
	}
	decode := o.set['d']

	process := func(r io.Reader) error {
		data, err := readAllContext(ctx, r)
		if err != nil {
			return err
		}
		if decode {
			// Strip any whitespace/newlines as base64 utility permits newlines in input
			cleaned := strings.Map(func(r rune) rune {
				if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
					return -1
				}
				return r
			}, string(data))

			decoded, err := base64.StdEncoding.DecodeString(cleaned)
			if err != nil {
				// Try raw/unpadded decoding
				decoded, err = base64.RawStdEncoding.DecodeString(cleaned)
			}
			if err != nil {
				return fmt.Errorf("invalid input")
			}
			_, err = ec.Stdout.Write(decoded)
			return err
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		_, err = fmt.Fprintln(ec.Stdout, encoded)
		return err
	}

	if len(o.args) == 0 {
		if err := process(ec.StdinReader()); err != nil {
			return ec.Fail(1, "base64: %v\n", err)
		}
		return nil
	}

	exit := 0
	for _, name := range o.args {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if name == "-" {
			if err := process(ec.StdinReader()); err != nil {
				ec.Errorf("base64: %v\n", err)
				exit = 1
			}
			continue
		}
		data, err := ec.FS.ReadFile(ec.Resolve(name))
		if err != nil {
			ec.Errorf("base64: %s: %s\n", name, errMsg(err))
			exit = 1
			continue
		}
		if err := process(bytes.NewReader(data)); err != nil {
			ec.Errorf("base64: %v\n", err)
			exit = 1
		}
	}
	return command.Exit(exit)
}

// Base64 is the built-in base64 encoding and decoding command.
var Base64 command.Command = base64Cmd{}

