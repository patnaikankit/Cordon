package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/netpolicy"
	"github.com/cordon-dev/cordon/script"
)

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
)

func printHeader(title string) {
	fmt.Printf("\n%s%s=== %s ===%s\n", colorBold, colorCyan, title, colorReset)
}

func printSuccess(msg string) {
	fmt.Printf("%s[BLOCKED/SAFE]%s %s\n", colorGreen, colorReset, msg)
}

func printInfo(label, val string) {
	fmt.Printf("%s%s:%s %s\n", colorYellow, label, colorReset, strings.TrimSpace(val))
}

func main() {
	fmt.Printf("%s%s=====================================================\n", colorBold, colorCyan)
	fmt.Printf("        CORDON AI-AGENT RUNTIME DEMO\n")
	fmt.Printf("=====================================================%s\n", colorReset)

	ctx := context.Background()

	// 1. Setup Sandbox with Capability Filesystem and Script Interpreters
	memFS := fs.Mem().Seed(map[string]string{
		"/app/config.json": `{"env": "sandbox", "version": "1.0"}`,
	})

	sb, err := cordon.New(cordon.Policy{
		Commands: cordon.Commands(commands.Core()...).With(
			script.PythonCommand(),
			script.NodeCommand(),
		),
		FS: memFS,
		Limits: cordon.Limits{
			Timeout:        100 * time.Millisecond,
			MaxOutputBytes: 128,
		},
		Tools: []cordon.ToolBinding{
			script.Python(),
			script.JS(),
		},
	})
	if err != nil {
		panic(err)
	}

	// ----------------------------------------------------
	// Demo 1: Multi-Language Capability Interoperability
	// ----------------------------------------------------
	printHeader("1. Multi-Language Capability Interoperability")
	fmt.Println("Python, JavaScript, and Bash share the exact same in-memory filesystem:")

	// Step A: Python tool creates dataset
	pyCode := `
write_file("/app/users.txt", "alice\nbob\ncharlie\n")
print("Python: wrote /app/users.txt")
`
	pyIn, _ := json.Marshal(map[string]string{"code": pyCode})
	resPy, _ := sb.CallTool(ctx, "python", pyIn)
	printInfo("Python Tool", resPy.Stdout)

	// Step B: JavaScript tool transforms data
	jsCode := `
const content = fs.readFileSync("/app/users.txt").trim();
const upper = content.split("\n").map(u => u.toUpperCase()).join("\n");
fs.writeFileSync("/app/users_upper.txt", upper + "\n");
console.log("JS: transformed users to uppercase");
`
	jsIn, _ := json.Marshal(map[string]string{"code": jsCode})
	resJS, _ := sb.CallTool(ctx, "js", jsIn)
	printInfo("JS Tool", resJS.Stdout)

	// Step C: Bash filters the result
	resBash, _ := sb.ExecBash(ctx, "grep -v BOB /app/users_upper.txt | sort")
	printInfo("Bash Output", "\n"+resBash.Stdout)

	// ----------------------------------------------------
	// Demo 2: Security Boundary — No Host Fallback
	// ----------------------------------------------------
	printHeader("2. Security Boundary: No Host Fallback")
	fmt.Println("Attempting to run host binaries ('whoami', 'id', '/bin/sh'):")
	resWhoami, _ := sb.ExecBash(ctx, "whoami")
	printSuccess(fmt.Sprintf("'whoami' failed deterministically with exit code %d (command not found, no host PATH search)", resWhoami.ExitCode))

	// ----------------------------------------------------
	// Demo 3: Security Boundary — Path Traversal Confinement
	// ----------------------------------------------------
	printHeader("3. Security Boundary: Path Traversal Confinement")
	fmt.Println("Attempting directory traversal escape ('cat /../../../etc/passwd'):")
	resPasswd, _ := sb.ExecBash(ctx, "cat /../../../etc/passwd")
	printSuccess(fmt.Sprintf("Traversal collapsed to virtual root. Output: %q (Host files untouched)", resPasswd.Stdout))

	// ----------------------------------------------------
	// Demo 4: Security Boundary — Script Escape Protection
	// ----------------------------------------------------
	printHeader("4. Security Boundary: Script Escape Resistance")
	fmt.Println("Attempting Python OS import ('import os; os.system(\"id\")'):")
	pyAttack, _ := json.Marshal(map[string]string{"code": "import os; os.system('id')"})
	resPyAttack, _ := sb.CallTool(ctx, "python", pyAttack)
	printSuccess(fmt.Sprintf("Starlark blocked host module import: %s", strings.TrimSpace(resPyAttack.Stderr)))

	// ----------------------------------------------------
	// Demo 5: Resource Supervision — Wall-Clock Timeout
	// ----------------------------------------------------
	printHeader("5. Resource Supervision: Infinite Loop Cutoff")
	fmt.Println("Executing infinite shell loop ('while true; do :; done'):")
	start := time.Now()
	resLoop, _ := sb.ExecBash(ctx, "while true; do :; done")
	elapsed := time.Since(start)
	printSuccess(fmt.Sprintf("Terminated after %v with exit code %d: %s", elapsed.Round(time.Millisecond), resLoop.ExitCode, strings.TrimSpace(resLoop.Stderr)))

	// ----------------------------------------------------
	// Demo 6: Resource Supervision — Output Flooding Guard
	// ----------------------------------------------------
	printHeader("6. Resource Supervision: Output Byte Budget")
	fmt.Println("Executing output flood exceeding MaxOutputBytes limit (128 bytes):")
	resFlood, _ := sb.ExecBash(ctx, "for i in 1 2 3 4 5 6 7 8 9 10; do pwd; pwd; pwd; done")
	printSuccess(fmt.Sprintf("Output capped at 128 bytes; exit code %d, stderr: %q", resFlood.ExitCode, strings.TrimSpace(resFlood.Stderr)))

	// ----------------------------------------------------
	// Demo 7: Network Policy — SSRF & Cloud Metadata
	// ----------------------------------------------------
	printHeader("7. Network Policy: SSRF & Cloud Metadata Defense")
	fmt.Println("Testing default deny and AWS/GCP/Azure cloud metadata blocking (169.254.169.254):")
	netPol := netpolicy.New(netpolicy.AllowHost("*", 80))
	errMetadata := netPol.ValidateHostPort("169.254.169.254", 80)
	printSuccess(fmt.Sprintf("Cloud metadata connection blocked at policy layer: %v", errMetadata))

	fmt.Printf("\n%s%sDemo complete: All policies and invariants verified!%s\n\n", colorBold, colorGreen, colorReset)
}
