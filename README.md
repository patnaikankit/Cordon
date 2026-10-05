# Cordon

**A policy-driven Go runtime for safely executing AI-agent tool calls with explicit filesystem, network, and resource policies.**

```text
AI SDK / Agent (OpenAI, Anthropic, Custom)
      │
      ▼
Tools() + CallTool()
      │
      ▼
Sandbox Call Supervisor
  ├── Child Context / Timeout / Cooperative Cancellation
  ├── Shared Stdout/Stderr Output Byte Budget
  └── Command Count Budget
      │
 ┌────┴──────────────────────────┐
 │                               │
bash (real syntax, safe cmds)  custom tools (Go capabilities)
 │                               │
 └────┬──────────────────────────┘
      │
Capability-Bound Resource Plane
  ├── Virtual Filesystem (MemFS / Quotas / Refuse / Hide)
  ├── Network Policy (SSRF Guard / Host Whitelisting)
  └── Isolated Environment (No Host PATH / No Host Leaks)
```

The core promise of Cordon is **policy enforcement**. Rather than trusting host binaries, unconstrained network sockets, or raw filesystem paths, Cordon interposes an explicit policy plane between an AI agent's requested actions and the host environment.

---

## Key Guarantees

- **Deny-by-Default**: The zero value of `cordon.Policy{}` permits no commands, no host filesystem access, and no outbound network connections.
- **Real Bash, Zero Host Fallback**: Powered by a pure-Go shell interpreter (`github.com/goccy/sh/v3`), Cordon supports real bash syntax—pipes, redirections, subshells, environment variables, globbing, and control flow—while dispatching exclusively to an allowlist of Go commands. Unregistered commands return `command not found` (exit code 127) and **never touch the host `$PATH` or host binaries**.
- **Capability-Based Filesystem**: Virtual in-memory filesystem (`fs.Mem()`) with rooted path confinement (`Clean`), byte quotas (`MaxFileBytes`, `MaxBytes`), and declarative path rules:
  - `Refuse(globs...)`: Denies access with `io/fs.ErrPermission`.
  - `Hide(globs...)`: Makes paths appear nonexistent (`io/fs.ErrNotExist`) and omits them from directory listings.
  - Subtree protections: Deleting or renaming an ancestor directory cannot bypass child rules.
- **Unified Resource Plane**: Bash commands and registered custom tools operate on the exact same capability filesystem instance and network rules.
- **Per-Call Supervisor & Resource Budgets**: Every call owns its own child context, timeout deadline, combined stdout/stderr byte cap (`MaxOutputBytes`), and command execution count (`MaxCommandCount`). Budgets and state are strictly isolated and never leak across calls.
- **SDK Neutrality**: Tools and execution results follow standard JSON Schemas and clean Go types (`Tool`, `Result`), integrating with any AI SDK (OpenAI, Anthropic, LangChain Go, etc.) without proprietary adapters.

---

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/command"
	"github.com/cordon-dev/cordon/fs"
)

func main() {
	// Define allowlisted commands (using Go implementations)
	echoCmd := command.New("echo", func(ctx context.Context, ec *command.Context) error {
		fmt.Fprintln(ec.Stdout, ec.Args[1:])
		return nil
	})

	// Configure sandbox policy
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(echoCmd),
		FS: fs.Mem().
			Seed(map[string]string{
				"/app/data.txt": "sample data\n",
			}).
			Refuse("/etc/**", "*.key"),
		Limits: cordon.Limits{
			Timeout:         5 * time.Second,
			MaxOutputBytes:  1024 * 1024, // 1 MiB
			MaxCommandCount: 100,
		},
	})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	// Execute bash tool calls safely
	result, err := sb.ExecBash(ctx, "echo 'hello from cordon' > /app/out.txt")
	if err != nil || result.IsError {
		fmt.Printf("Execution failed: %s\n", result.Stderr)
		return
	}

	fmt.Println("Command executed successfully!")
}
```

---

## Integration with AI Agents

Cordon provides SDK-neutral descriptors for tool discovery and execution:

```go
// 1. Advertise available tools to the AI agent
tools := sb.Tools()
// Returns []Tool{ { Name: "bash", Description: "...", InputSchema: ... } }

// 2. Dispatch a tool invocation requested by the AI model
toolCallJSON := []byte(`{"command": "echo 'Hello AI' | uppercase"}`)
res, err := sb.CallTool(ctx, "bash", toolCallJSON)
if err != nil {
	// Dispatch error (invalid JSON or unknown tool name)
	log.Fatalf("Dispatch error: %v", err)
}

if res.IsError {
	// Tool execution failure (non-zero exit, timeout, or limit exceeded)
	fmt.Printf("Exit Code: %d\nStderr: %s\n", res.ExitCode, res.Stderr)
} else {
	fmt.Printf("Output:\n%s\n", res.Stdout)
}
```

---

## Custom Tools & Go Capabilities

Rather than exposing internal sandbox structures to custom Go tools, Cordon provides narrow capability handles:

```go
caps := sb.Resources()

// Custom tools access the same capability-bound filesystem
err := caps.FS().WriteFile("/tool.log", []byte("event"), 0644)

// Custom tools derive contexts with the sandbox timeout
toolCtx, cancel := caps.Context(context.Background())
defer cancel()
```

---

## Architecture and Repository Layout

```text
cordon/
  sandbox.go          Sandbox construction and top-level lifecycle
  policy.go           Public Policy, CommandPolicy, and Limits types
  tool.go             SDK-neutral Tool and Result types
  call.go             Per-call supervisor, output budgets, and timeouts
  capability.go       Capability bundles for custom Go tools
  command/            Command interface, execution Context, and Registry
  fs/                 Capability filesystem: MemFS, rooted path confinement, access rules
  netpolicy/          Network policy stubs (SSRF protection & dialing rules)
  shell/              Parser/interpreter integration (goccy/sh, dev files, exec middleware)
  docs/               Threat model, invariants, and architectural specifications
```

---

## Security Model and Invariants

See [`docs/threat-model.md`](docs/threat-model.md) for full threat modeling and security invariants:

| Invariant | Description |
| :--- | :--- |
| **No Host Fallback** | Host binaries and host `$PATH` lookups never occur; unregistered commands exit 127. |
| **Deny-by-Default** | Zero policy runs no commands and grants zero filesystem or network access. |
| **Path Confinement** | Traversal sequences (`../../`) collapse to virtual root `/`; escapes are impossible. |
| **Call Lifecycle** | Calls strictly own their context and byte budgets; no cross-call budget leakage. |
| **Device Virtualization** | `/dev/null`, `/dev/stdout`, `/dev/stderr` are synthetic in-memory handles. |

---


## Running Tests

Run the full test suite with race detection enabled:

```bash
go test -v -race ./...
```
