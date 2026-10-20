// Command kyurem is the Kyurem gateway entrypoint with three subcommands:
//
//	serve      run the gateway (default)
//	validate   load and validate a config file without starting
//	version    print version and compatibility information
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kyurem-project/kyurem/gateway/internal/config"
	"github.com/kyurem-project/kyurem/gateway/internal/logging"
	"github.com/kyurem-project/kyurem/gateway/internal/server"
	"github.com/kyurem-project/kyurem/gateway/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stdout)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "validate":
		os.Exit(runValidate(os.Args[2:]))
	case "version":
		os.Exit(runVersion(os.Args[2:]))
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `kyurem — A2A protocol gateway

Usage:
  kyurem serve      --config <path>     start the gateway
  kyurem validate   --config <path>     validate a config file without starting
  kyurem version                        print version information

`)
}

// runServe loads config, wires the server and blocks until SIGINT/SIGTERM,
// draining in-flight streams within the configured shutdown grace (GW-7).
func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "kyurem.yaml", "path to the configuration file")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}

	logger := logging.New(cfg.Logging)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := server.New(cfg, logger)
	if err != nil {
		logger.Error("server setup failed", "err", err)
		return 1
	}
	// Start returns only after full drain/close; on context cancel it runs
	// the graceful path and reports the final state.
	if err := srv.Start(ctx); err != nil {
		logger.Error("server exited with error", "err", err)
		return 1
	}
	return 0
}

// runValidate validates a config fully (defaults + env overrides) without
// starting listeners, printing a summary on success (PRD §8).
func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	configPath := fs.String("config", "kyurem.yaml", "path to the configuration file")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration:\n%v\n", err)
		return 1
	}

	summary := map[string]any{
		"ok":                 true,
		"configuration_file": *configPath,
		"agents":             len(cfg.Agents),
		"a2a_listen":         cfg.Listeners.A2A.Addr,
		"admin_listen":       cfg.Listeners.Admin.Addr,
	}
	out, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Fprintln(os.Stdout, string(out))
	return 0
}

// runVersion prints product and protocol compatibility in one line + JSON.
func runVersion(args []string) int {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print as JSON")
	_ = fs.Parse(args)

	info := map[string]string{
		"version":            version.Version,
		"a2a_spec_version":   version.SpecVersion,
		"a2a_go_sdk_version": version.GoVersion(),
	}
	if *asJSON {
		out, _ := json.MarshalIndent(info, "", "  ")
		fmt.Println(string(out))
		return 0
	}
	fmt.Printf("kyurem %s (A2A spec %s, a2a-go %s)\n", version.Version, version.SpecVersion, version.GoVersion())
	return 0
}
