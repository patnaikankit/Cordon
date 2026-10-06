# Changelog

All notable changes to Cordon will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-10-06

### Added
- **Core Sandbox Engine**: In-process policy supervisor enforcing allowlisted commands, virtual filesystem boundaries, network egress rules, and execution budgets.
- **Capability-Based Filesystem (`fs`)**:
  - In-memory filesystem (`MemFS`) with normalized path traversal confinement (`fs.Clean`, `fs.Resolve`).
  - Path access control policies (`Refuse`, `Hide`) and storage byte quotas (`MaxFileBytes`, `MaxBytes`).
  - Rooted host mounts (`HostFS`) with strict symlink confinement against host escaping.
  - In-memory Copy-on-Write overlay filesystem (`OverlayFS`) with whiteout marker tracking for deletions and moves.
- **Pure-Go Shell & Core Command Library**:
  - Sandboxed Bash execution using `goccy/sh` without host binary fallback (exit code 127 on unknown commands).
  - Virtual `/dev/null`, `/dev/stdout`, and `/dev/stderr` handles.
  - Core POSIX utilities in pure Go: `cat`, `ls`, `pwd`, `mkdir`, `rm`, `cp`, `mv`, `head`, `tail`, `wc`, `grep`, `sort`, `uniq`, `cut`, `tr`, `base64`, `sha256sum`, and `curl`.
- **SSRF-Resistant Network Policy (`netpolicy`)**:
  - Deny-by-default egress policy with fine-grained host, port, and HTTP method matching.
  - Automatic blocking of RFC 1918 private ranges, loopback, link-local, and cloud metadata IPs (`169.254.169.254`).
  - DNS pre-resolution and concrete-IP pinning against DNS rebinding (TOCTOU) attacks.
  - Cross-host redirect blocking by default.
  - Configurable response body limits (`MaxResponseBytes`) and automatic request header injection.
- **Resource Supervision & Budgets**:
  - Per-call wall-clock timeouts with cooperative context cancellation.
  - Per-call command invocation limits (`MaxCommandCount`) to terminate runaway loops and subshells.
  - Output byte budgets (`MaxOutputBytes`) protecting LLM contexts against output flooding.
  - Detached goroutine write guards preventing resource leaks after call termination.
- **Optional Pure-Go Script Interpreters (`script`)**:
  - Sandboxed Python runtime via Google's Starlark with capability filesystem and networking builtins.
  - Sandboxed JavaScript runtime via Goja with stack-depth caps and sandboxed `fs`/`fetch`/`console` globals.
  - Zero host imports, sockets, or subprocesses.
- **SDK-Neutral Tool Interface & Clients**:
  - SDK-agnostic `Tools()` discovery and `CallTool()` JSON dispatch.
  - OpenAI and Anthropic Claude tool adapters (`examples/adapters/`).
  - Standalone HTTP/JSON RPC daemon (`cmd/cordon-server`).
  - Pure-Python client (`sdk/python/`) with zero third-party dependencies.
- **Security Hardening & Evidence**:
  - Go native fuzz tests (`FuzzPathClean`, `FuzzHostMatch`, `FuzzShellInput`).
  - Exhaustive adversarial test suite covering traversal, symlink escapes, SSRF, redirects, resource exhaustion, and host fallback.
  - Reproducible, static binary build script (`scripts/build.sh`).
  - Performance benchmarks measuring microsecond-level construction, execution, and throughput.
