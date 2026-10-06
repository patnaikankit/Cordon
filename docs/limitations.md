# Cordon Limitations & Security Boundaries

Cordon is a policy-driven runtime for AI-agent tool execution in pure Go. To provide an honest and actionable threat model, this document explicitly details what Cordon is designed to protect against, and what is intentionally out of scope.

---

## What Cordon Protects Against

1. **Host Binary & PATH Fallback**:
   - Shell commands never execute arbitrary host binaries or inherit host `$PATH`.
   - Unregistered commands fail deterministically with exit code 127 (`command not found`).

2. **Filesystem Traversal & Symlink Escape**:
   - Path normalization collapses `../` sequences to virtual root `/`.
   - Host mounts (`HostFS`) evaluate real symlink destinations via canonical path resolution; symlinks resolving outside the mount root are rejected with `ErrPermission`.
   - Copy-on-Write overlays (`OverlayFS`) keep all modifications in memory and never mutate host files.

3. **Server-Side Request Forgery (SSRF) & DNS Rebinding**:
   - Deny-by-default network policy blocks all outbound traffic unless explicitly allowlisted.
   - Private IP ranges (RFC 1918), loopback, link-local, carrier-grade NAT, and cloud metadata endpoints (`169.254.169.254`, `[fd00:ec2::254]`, `metadata.google.internal`) are blocked.
   - DNS pre-resolution verifies concrete target IP addresses and pins them during dialing, closing DNS rebinding TOCTOU attack windows.
   - Cross-host redirects are blocked by default.

4. **Resource Exhaustion & Infinite Loops**:
   - Every call is bounded by a wall-clock timeout (`Limits.Timeout`).
   - Runaway command invocations in loops or pipelines are terminated by `Limits.MaxCommandCount`.
   - Output generation is capped by `Limits.MaxOutputBytes`, truncating excess output and protecting downstream LLM contexts from token-exhaustion DOS attacks.

---

## Out-of-Scope Limitations

The following capabilities are deliberately out of scope for Cordon's in-process Go architecture:

### 1. Untrusted Native Binaries (ELF / Mach-O / PE)
- Cordon runs pure Go commands in-process. It does **not** compile or execute arbitrary untrusted native C/C++/Rust/assembly binaries or shared libraries (`.so`, `.dylib`, `.dll`).
- If an agent needs to execute arbitrary untrusted compiled binaries, run Cordon inside an OS container (Docker / Podman) or microVM (Firecracker / gVisor).

### 2. Full POSIX Kernel Compatibility
- Cordon provides pure-Go reimplementations of core POSIX utilities (`cat`, `ls`, `grep`, `wc`, `sort`, `uniq`, etc.) and bash syntax (`goccy/sh`).
- Complex kernel-level POSIX features—such as process signals (`SIGSTOP`, `SIGCONT`), job control, FIFOs (`mkfifo`), raw pseudo-terminals (`pty`), `/proc` filesystem emulation, or memory-mapped file handles (`mmap`)—are not supported.

### 3. Hard Hardware Memory Limits for Custom Native Go Extensions
- Cordon enforces memory bounds on shell variable expansions, Starlark/Goja interpreter limits, and output stream buffers.
- However, arbitrary custom Go extensions registered by users that allocate memory directly in the Go heap cannot have hard kernel-level cgroup memory limits applied to a single goroutine without OS process isolation.

### 4. Arbitrary Package Management (`pip install`, `npm install`)
- The Python and JavaScript interpreters are hermetic, pure-Go embedded runtimes (Starlark and Goja).
- They do not support installing arbitrary CPython packages with native wheels or arbitrary npm packages with native bindings that invoke C compilers or package managers.

---

## Recommended Deployment Model

For defense-in-depth in production environments:
- **Inner Sandbox (Cordon)**: Enforces fine-grained AI tool allowlisting, bash command filtering, output token budgets, capability filesystems, and SSRF network policy.
- **Outer Sandbox (Container / MicroVM)**: Enforces OS-level cgroup memory/CPU limits and kernel syscall filters (seccomp).
