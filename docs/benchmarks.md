# Cordon Performance Benchmarks

Measured on Apple M4 (`darwin/arm64`, Go 1.25):

```text
BenchmarkSandbox_Construction-10            345,081 ops       3.56 µs/op      3,239 B/op       22 allocs/op
BenchmarkSandbox_EmptyExecBash-10           639,439 ops       1.91 µs/op        728 B/op       15 allocs/op
BenchmarkSandbox_EchoCommand-10              24,829 ops      48.68 µs/op     11,888 B/op       77 allocs/op
BenchmarkSandbox_Pipeline-10                  6,598 ops     160.43 µs/op    129,342 B/op      222 allocs/op
BenchmarkSandbox_ConcurrentThroughput-10     59,754 ops      22.70 µs/op     11,894 B/op       77 allocs/op
BenchmarkMemFS_ReadWrite-10               4,074,531 ops     328.40 ns/op    200.98 MB/s         3 allocs/op
BenchmarkNetPolicy_Validation-10          3,714,598 ops     307.80 ns/op         48 B/op        1 allocs/op
BenchmarkPython_StartupAndExec-10            34,278 ops      34.06 µs/op      8,431 B/op      147 allocs/op
BenchmarkJS_StartupAndExec-10                23,274 ops      54.12 µs/op     26,607 B/op      343 allocs/op
```

## Key Latency & Resource Takeaways

1. **Sub-4 Microsecond Sandbox Construction**:
   Creating a fresh `Sandbox` instance with capability filesystem and command registry takes **3.56 µs** and only **3.2 KB** of memory, enabling per-agent or per-session sandbox pooling without container cold-start penalties.

2. **Ultra-Low Command Dispatch Latency**:
   A single shell command executes in **~48 µs**, while a 3-stage pipeline (`cat | grep | sort`) completes in **~160 µs**.

3. **High Concurrent Throughput**:
   Under parallel multi-goroutine load, single command execution sustains **~44,000 ops/second** (`22.7 µs/op`) with zero budget contention or cross-call leakage.

4. **In-Memory Filesystem Performance**:
   `MemFS` achieves over **200 MB/s** read/write bandwidth with **328 ns** round-trip file operations and minimal GC overhead (3 allocations per operation).

5. **Sub-Millisecond Script Interpreters**:
   Hermetic Python (Starlark) script evaluation completes in **~34 µs**, and ECMAScript (Goja) completes in **~54 µs**, offering instant execution compared to external Python/Node subprocesses (typically 30–100 ms).
