package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/internal/status"
)

// Define a custom tool schema
const counterSchema = `{
	"type": "object",
	"properties": {
		"file": {
			"type": "string",
			"description": "Path to file whose lines should be counted."
		}
	},
	"required": ["file"]
}`

// lineCounterTool creates a typed custom tool operating strictly within the sandbox capabilities.
func lineCounterTool() cordon.ToolBinding {
	return cordon.ToolBinding{
		Tool: cordon.Tool{
			Name:        "count_lines",
			Description: "Counts the number of non-empty lines in a file within the sandbox filesystem.",
			InputSchema: json.RawMessage(counterSchema),
		},
		Handler: func(ctx context.Context, input json.RawMessage, caps cordon.Capabilities, stdout, stderr io.Writer) (int, error) {
			var in struct {
				File string `json:"file"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return 0, fmt.Errorf("%w: %v", status.ErrMalformedInput, err)
			}
			if in.File == "" {
				return 0, fmt.Errorf("%w: 'file' parameter is required", status.ErrMalformedInput)
			}

			// Read file using the capability filesystem handle
			data, err := caps.FS().ReadFile(in.File)
			if err != nil {
				fmt.Fprintf(stderr, "error reading %s: %v\n", in.File, err)
				return status.StatusError, nil
			}

			// Compute count
			count := 0
			for _, b := range data {
				if b == '\n' {
					count++
				}
			}

			fmt.Fprintf(stdout, "File %s contains %d lines\n", in.File, count)
			return status.StatusOK, nil
		},
	}
}

func main() {
	// Create Sandbox with custom tool and seeded filesystem
	mem := fs.Mem().Seed(map[string]string{
		"/app/data.csv": "col1,col2\nval1,val2\nval3,val4\n",
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       mem,
		Tools: []cordon.ToolBinding{
			lineCounterTool(),
		},
	})
	if err != nil {
		log.Fatalf("failed to create sandbox: %v", err)
	}

	// 1. Discover tools
	fmt.Println("=== Advertised Tools ===")
	for _, tool := range sb.Tools() {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	}

	// 2. Call custom tool via CallTool JSON interface
	ctx := context.Background()
	callInput := []byte(`{"file": "/app/data.csv"}`)

	fmt.Println("\n=== Executing count_lines Tool ===")
	res, err := sb.CallTool(ctx, "count_lines", callInput)
	if err != nil {
		log.Fatalf("tool dispatch error: %v", err)
	}
	if res.IsError {
		log.Fatalf("tool execution error: %s", res.Stderr)
	}
	fmt.Printf("Result: %s", res.Stdout)

	// 3. Bash execution sharing the same filesystem
	fmt.Println("\n=== Bash Execution ===")
	bashRes, err := sb.ExecBash(ctx, "cat /app/data.csv | wc -l")
	if err != nil || bashRes.IsError {
		log.Fatalf("bash error: %v", err)
	}
	fmt.Printf("Bash Line Count: %s", bashRes.Stdout)
}
