package benchmark_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/netpolicy"
	"github.com/cordon-dev/cordon/script"
)

// BenchmarkSandbox_Construction measures sandbox initialization latency and memory allocations.
func BenchmarkSandbox_Construction(b *testing.B) {
	policy := cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       fs.Mem(),
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sb, err := cordon.New(policy)
		if err != nil {
			b.Fatalf("New failed: %v", err)
		}
		_ = sb
	}
}

// BenchmarkSandbox_EmptyExecBash measures empty command dispatch overhead.
func BenchmarkSandbox_EmptyExecBash(b *testing.B) {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       fs.Mem(),
	})
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := sb.ExecBash(ctx, "")
		if err != nil || res.IsError {
			b.Fatalf("ExecBash failed: %v, stderr: %s", err, res.Stderr)
		}
	}
}

// BenchmarkSandbox_EchoCommand measures latency of running a single command.
func BenchmarkSandbox_EchoCommand(b *testing.B) {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       fs.Mem(),
	})
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := sb.ExecBash(ctx, "pwd")
		if err != nil || res.IsError {
			b.Fatalf("ExecBash failed: %v, stderr: %s", err, res.Stderr)
		}
	}
}

// BenchmarkSandbox_Pipeline measures multi-stage shell pipeline latency.
func BenchmarkSandbox_Pipeline(b *testing.B) {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS: fs.Mem().Seed(map[string]string{
			"/data.txt": "apple\nbanana\ncherry\napricot\n",
		}),
	})
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := sb.ExecBash(ctx, "cat /data.txt | grep a | sort")
		if err != nil || res.IsError {
			b.Fatalf("ExecBash pipeline failed: %v, stderr: %s", err, res.Stderr)
		}
	}
}

// BenchmarkSandbox_ConcurrentThroughput measures throughput under concurrent parallel execution.
func BenchmarkSandbox_ConcurrentThroughput(b *testing.B) {
	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...),
		FS:       fs.Mem(),
	})
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			res, err := sb.ExecBash(ctx, "pwd")
			if err != nil || res.IsError {
				b.Errorf("ExecBash concurrent failed: %v", err)
			}
		}
	})
}

// BenchmarkMemFS_ReadWrite measures in-memory filesystem read/write throughput.
func BenchmarkMemFS_ReadWrite(b *testing.B) {
	mem := fs.NewMemFS()
	data := []byte("benchmark data payload containing some structured test characters\n")

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := mem.WriteFile("/bench.txt", data, 0o644); err != nil {
			b.Fatalf("WriteFile failed: %v", err)
		}
		read, err := mem.ReadFile("/bench.txt")
		if err != nil || len(read) != len(data) {
			b.Fatalf("ReadFile failed: %v", err)
		}
	}
}

// BenchmarkNetPolicy_Validation measures network policy validation overhead.
func BenchmarkNetPolicy_Validation(b *testing.B) {
	pol := netpolicy.New(
		netpolicy.AllowHost("api.cordon.dev", 443),
		netpolicy.AllowHost("*.github.com", 443),
		netpolicy.DenyRule(netpolicy.Rule{Host: "evil.github.com"}),
	)
	targetURL, _ := url.Parse("https://api.cordon.dev/v1/resource")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := pol.ValidateURL(targetURL, "GET"); err != nil {
			b.Fatalf("ValidateURL failed: %v", err)
		}
	}
}

// BenchmarkPython_StartupAndExec measures Python/Starlark startup and script execution.
func BenchmarkPython_StartupAndExec(b *testing.B) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{
			script.Python(),
		},
	})
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}
	ctx := context.Background()
	input, _ := json.Marshal(map[string]string{
		"code": "x = [i * 2 for i in range(10)]\n",
	})

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := sb.CallTool(ctx, "python", input)
		if err != nil || res.IsError {
			b.Fatalf("python execution failed: %v, stderr: %s", err, res.Stderr)
		}
	}
}

// BenchmarkJS_StartupAndExec measures JavaScript/Goja startup and script execution.
func BenchmarkJS_StartupAndExec(b *testing.B) {
	sb, err := cordon.New(cordon.Policy{
		Tools: []cordon.ToolBinding{
			script.JS(),
		},
	})
	if err != nil {
		b.Fatalf("New failed: %v", err)
	}
	ctx := context.Background()
	input, _ := json.Marshal(map[string]string{
		"code": "const x = [1, 2, 3, 4, 5].map(n => n * 2);\n",
	})

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		res, err := sb.CallTool(ctx, "js", input)
		if err != nil || res.IsError {
			b.Fatalf("js execution failed: %v, stderr: %s", err, res.Stderr)
		}
	}
}
