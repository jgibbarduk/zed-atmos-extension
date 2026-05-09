package main

import (
	"flag"
	"log"
	"log/slog"
	"os"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/handler"
	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/proxy"
)

func main() {
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	envDebug := os.Getenv("ATMOS_LSP_BRIDGE_DEBUG") != ""
	if *debug || envDebug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	}

	slog.Info("atmos-lsp-bridge starting")

	idx, err := index.New("")
	if err != nil {
		log.Fatalf("create index: %v", err)
	}
	defer idx.Close()

	p, err := proxy.New("atmos")
	if err != nil {
		slog.Warn("atmos lsp not found, running in bridge-only mode", "error", err)
		p = proxy.NewNop()
	}
	defer p.Close()

	h := handler.New(idx, p)

	if err := p.Run(os.Stdin, os.Stdout, h); err != nil {
		log.Fatalf("run proxy: %v", err)
	}
}
