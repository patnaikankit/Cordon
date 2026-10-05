# Cordon Threat Model and Acceptance Criteria (Phase 0)

## 1. System Overview

Cordon is an in-process, policy-enforced Go runtime designed for AI agents executing tool calls (such as shell commands, scripts, and custom extensions). Rather than trusting host binaries, unconstrained network sockets, or raw filesystem paths, Cordon interposes an explicit policy plane between an AI agent's requested actions and the host environment.

## 2. Trust Boundaries and Actors

```
┌────────────────────────────────────────────────────────┐
│                   Untrusted / Semi-Trusted             │
│            AI Model / Agent / Prompt Payloads          │
└───────────────────────────┬────────────────────────────┘
                            │ Tool invocation (JSON)
                            ▼
┌────────────────────────────────────────────────────────┐
│            Cordon Sandbox Boundary (In-Process)        │
│                                                        │
│  [Call Supervisor]                                     │
│   ├── Context / Timeout / Cooperative Cancellation     │
│   ├── Command Count Budget                             │
│   └── Shared Stdout/Stderr Output Byte Budget          │
│                                                        │
│  [Execution Dispatcher]                                │
│   ├── Tool Registry (bash, python, custom tools)       │
│   └── Command Registry (allowlisted Go implementations)│
│                                                        │
│  [Capability-Bound Resource Plane]                     │
│   ├── Rooted / Virtual Filesystem (MemFS / Overlay)    │
│   └── Network Policy Engine (SSRF guard, host allow)   │
└───────────────────────────┬────────────────────────────┘
                            │ Strictly policy-governed
                            ▼
┌────────────────────────────────────────────────────────┐
│                 Host OS & Remote Services              │
│  (Host PATH, /etc, /proc, private network, loopback)   │
└────────────────────────────────────────────────────────┘
```

### Actors & Components
- **AI Agent / Caller**: Emits tool execution requests (e.g. bash commands or structured tool calls). The input is untrusted; it may contain prompt injection, malicious shell commands, recursive fork-bombs, path traversal sequences, or SSRF payloads.
- **Call Supervisor**: Owns the lifecycle of a single tool execution. Enforces wall-clock timeout, command count budget, combined output byte budget, and cancellation propagation.
- **Command & Tool Registry**: Dispatches tool calls strictly to explicitly allowed Go implementations. Never executes host binaries or falls back to the host PATH.
- **Resource Plane (Capabilities)**: Implements capability-based access to filesystems and networks. Prevents arbitrary host file access, enforces path normalization, and blocks SSRF / loopback dialing.

## 3. Threat Vectors and Mitigations

| Threat Vector | Attack Scenario | Cordon Mitigation |
| :--- | :--- | :--- |
| **Host Binary Execution** | Agent runs `bash -c "rm -rf /"` or invokes host binaries like `nc`, `bash`, `curl`, `sudo`. | No host PATH lookup. Only Go commands explicitly allowlisted in `Policy.Commands.Allow` exist. Host binaries yield "command not found". |
| **Filesystem Escape & Traversal** | Agent inputs paths like `../../etc/passwd` or creates symlinks pointing to host secrets. | All paths are resolved and confined inside the virtual root. Symlink dereferencing checks boundaries. Host filesystem is never mounted by default. |
| **Server-Side Request Forgery (SSRF)** | Agent curls `http://169.254.169.254` (cloud metadata) or `http://127.0.0.1:8080` (internal services). | Deny-by-default network policy. DNS resolution precedes dial checks; loopback, link-local, and private RFC1918 IPs are blocked by default. |
| **Denial of Service (DoS) - Output** | Agent runs `yes` or `cat /dev/urandom` producing infinite output. | Per-call synchronized `MaxOutputBytes` budget. Writing stops and error flag is set once budget is reached. |
| **Denial of Service (DoS) - CPU/Fork** | Agent runs infinite loops or fork bombs (`:(){ :\|:& };:`). | `MaxCommandCount` limits executed command invocations per call. Wall-clock `Timeout` terminates execution cooperatively. |
| **Cross-Call State Contamination** | A previous call's context or output budget leaks into subsequent calls. | Every call receives an isolated child context, independent output budget, and independent command counter. |
| **Panic / Host Process Crash** | Malformed script trips a runtime panic in an interpreter or tool. | Call supervisor recovers panics at the dispatch boundary, converting them to failure `Result` objects without crashing the host process. |

## 4. Security Invariants and Test Verification Matrix

| Invariant ID | Security Invariant | Planned Test Verification |
| :--- | :--- | :--- |
| **INV-01** | **No Host Fallback**: No command may fall back to the host PATH or host binary execution. | `TestSecurity_NoHostFallback`: Attempt running commands present on host system (`sh`, `env`, `whoami`, etc.) when not registered; verify `exit code 127` / command not found. |
| **INV-02** | **Deny-by-Default**: Zero-value `Policy{}` permits no commands, no host filesystem access, and no network access. | `TestSecurity_ZeroPolicyDeniesAll`: Execute commands and network dials under `Policy{}`; verify immediate rejection and zero leakage. |
| **INV-03** | **Path Confinement**: Every path is normalized and checked at the filesystem boundary; escape outside virtual root is impossible. | `TestSecurity_PathConfinement`: Pass `../../etc/passwd`, relative `..` sequences, and symlink targets outside root; verify all stay confined within root or error. |
| **INV-04** | **Unified Network Policy**: Every outbound network request passes through the same policy engine regardless of caller. | `TestSecurity_NetworkSSRFBlocked`: Verify attempts to access `127.0.0.1`, `169.254.169.254`, and non-allowlisted domains are denied across curl, HTTP client, and raw dials. |
| **INV-05** | **Call Lifecycle & Bounded Accounting**: Every call has an owner context, cooperative cancellation, and strictly bounded output and command count. | `TestSupervisor_BudgetsAndCancellation`: Verify that timeouts stop execution, output exceeding `MaxOutputBytes` is truncated, and command budgets are isolated per call. |

## 5. Scope Definition

### Commands in Scope for v1
The v1 command suite focuses on a lean, dependable core set of Go-native commands:
- **Filesystem & Navigation**: `cat`, `ls`, `mkdir`, `rm`, `cp`, `mv`
- **Text Processing**: `head`, `tail`, `wc`, `grep`, `sort`, `uniq`, `cut`, `tr`
- **Network**: `curl` (implemented only after network policy in Phase 6)

### Explicitly Out of Scope
- **Arbitrary Native Go Code Isolation**: Native Go functions compiled into the host binary run in-process and cannot be preemptively memory-capped or forcibly stopped if they spawn detached uncooperative goroutines. Untrusted native extensions belong in a separate process/container.
- **Full POSIX Compatibility**: Cordon implements real bash syntax via `mvdan.cc/sh`, but built-in commands implement only standard flags needed by agents, failing fast on unsupported flags rather than imitating full GNU coreutils edge cases.
- **Arbitrary Package Installation**: Commands like `apt`, `yum`, `pip install` (to host) are unsupported. Interpreters run sandboxed scripts without host package mutations.
- **Hard Memory Caps for In-Process Go Allocations**: Go runtime does not support per-goroutine heap quotas; memory bounding relies on input/output size caps, recursion depth limits, and WebAssembly linear memory limits for script runtimes.
