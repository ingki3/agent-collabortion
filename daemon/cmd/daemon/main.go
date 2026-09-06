// Command daemon runs on the user's machine (PLAN.md §2 stream D):
//
//	daemon pair <code> --server <url>   pair with the server, then probe
//	daemon run                          orphan sweep → probe → claim loop
//	daemon probe [--turn]               print the probe body (no server)
//	daemon version
//
// State lives in ~/.colab/daemon.json ($COLAB_DAEMON_CONFIG).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/config"
	"github.com/ingki3/agent-collabortion/daemon/internal/loop"
	"github.com/ingki3/agent-collabortion/daemon/internal/orphan"
	"github.com/ingki3/agent-collabortion/daemon/internal/probe"
)

var version = "dev"

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "version":
		fmt.Printf("colab-daemon %s (contracts %s, adapter %s@%s)\n", version, contracts.Version, contracts.ClaudeAgentACPPackage, contracts.ClaudeAgentACPPin)
	case "pair":
		err = cmdPair(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "probe":
		err = cmdProbe(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "colab-daemon:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: daemon pair <code> --server <url> | daemon run | daemon probe [--turn] | daemon version")
}

func cmdPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	server := fs.String("server", "", "server base URL (https://…)")
	cfgPath := fs.String("config", config.DefaultPath(), "config file")
	workdir := fs.String("workdir-root", "", "workdir root (default ~/.colab/work)")
	noTurn := fs.Bool("no-turn", false, "skip the PONG capability turn after pairing")
	var code string
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		code, args = args[0], args[1:]
	}
	_ = fs.Parse(args)
	if code == "" && fs.NArg() > 0 {
		code = fs.Arg(0)
	}
	if code == "" || *server == "" {
		return fmt.Errorf("usage: daemon pair <code> --server <url>")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *workdir != "" {
		cfg.WorkdirRoot = *workdir
	}
	host, _ := os.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := api.New(*server, "").Pair(ctx, api.PairRequest{PairingCode: code, Hostname: host, OS: runtime.GOOS, DaemonVersion: version})
	if err != nil {
		return fmt.Errorf("pair: %w", err)
	}
	cfg.ServerURL, cfg.RuntimeID, cfg.DaemonToken = *server, res.RuntimeID, res.DaemonToken
	if err := config.Save(*cfgPath, cfg); err != nil {
		return err
	}
	log.Printf("paired: runtime %s (config %s)", cfg.RuntimeID, *cfgPath)
	// daemon-protocol §2: probe right after pairing (S12 "CLI 감지 중 → 준비 완료")
	pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer pcancel()
	po := probe.Options{DaemonVersion: version, WorkdirRoot: cfg.WorkdirRoot, Turn: !*noTurn, ColabBin: cfg.ColabBin, Log: func(s string) { log.Print(s) }}
	p := probe.Run(pctx, po)
	if !p.ColabCLI.Present {
		log.Printf("warning: colab CLI not usable (%s) — agents have neither the MCP server nor the shell path", cfg.ColabBin)
	}
	if err := api.New(cfg.ServerURL, cfg.DaemonToken).Probe(pctx, cfg.RuntimeID, p); err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	for _, c := range p.Capabilities {
		log.Printf("runtime %s %s adapter=%s logged_in=%v models=%d", c.Kind, c.Version, c.AdapterVersion, c.LoggedIn, len(c.Models))
	}
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "config file")
	noTurn := fs.Bool("no-turn", false, "static probe only (no PONG turn)")
	_ = fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if !cfg.Paired() {
		return fmt.Errorf("not paired — run: daemon pair <code> --server <url>")
	}
	if err := os.MkdirAll(cfg.WorkdirRoot, 0o755); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := &loop.Daemon{
		Cfg:       cfg,
		Server:    api.New(cfg.ServerURL, cfg.DaemonToken),
		Version:   version,
		Orphans:   orphan.Store{Root: cfg.WorkdirRoot},
		Log:       log.Printf,
		ProbeTurn: !*noTurn,
	}
	log.Printf("colab-daemon %s runtime=%s server=%s workdir=%s capacity=%d", version, cfg.RuntimeID, cfg.ServerURL, cfg.WorkdirRoot, cfg.Capacity)
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	turn := fs.Bool("turn", false, "run the PONG turn per runtime")
	cfgPath := fs.String("config", config.DefaultPath(), "config file")
	_ = fs.Parse(args)
	cfg, _ := config.Load(*cfgPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	po := probe.Options{DaemonVersion: version, WorkdirRoot: cfg.WorkdirRoot, Turn: *turn, ColabBin: cfg.ColabBin, Log: func(s string) { log.Print(s) }}
	p := probe.Run(ctx, po)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}
