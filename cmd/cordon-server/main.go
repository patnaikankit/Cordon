package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cordon-dev/cordon"
	"github.com/cordon-dev/cordon/commands"
	"github.com/cordon-dev/cordon/fs"
	"github.com/cordon-dev/cordon/script"
	"github.com/cordon-dev/cordon/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "Server listen address")
	workDir := flag.String("workdir", "/", "Initial working directory in virtual FS")
	timeout := flag.Duration("timeout", 30*time.Second, "Default execution timeout per call")
	maxOutput := flag.Int64("max-output", 10*1024*1024, "Maximum output bytes per call")
	flag.Parse()

	cmdPolicy := cordon.Commands(commands.Core()...).With(
		script.PythonCommand(),
		script.NodeCommand(),
	)
	cmdPolicy.WorkDir = *workDir

	// Initialize sandbox with core commands, in-memory FS, and script interpreters
	sb, err := cordon.New(cordon.Policy{
		Commands: cmdPolicy,
		FS: fs.Mem(),
		Limits: cordon.Limits{
			Timeout:        *timeout,
			MaxOutputBytes: *maxOutput,
		},
		Tools: []cordon.ToolBinding{
			script.Python(),
			script.JS(),
		},
	})
	if err != nil {
		log.Fatalf("failed to create sandbox: %v", err)
	}

	handler := server.NewHandler(sb)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Printf("Cordon RPC server listening on %s\n", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	<-stop
	fmt.Println("\nShutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	fmt.Println("Server exited cleanly.")
}
