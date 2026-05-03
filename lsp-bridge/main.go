package main

import (
	"flag"
	"log"
	"log/slog"
	"os"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/handler"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/proxy"
)

func main() {
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	if *debug {
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
		log.Fatalf("create proxy: %v", err)
	}
	defer p.Close()

	h := handler.New(idx, p)

	if err := p.Run(os.Stdin, os.Stdout, h); err != nil {
		log.Fatalf("run proxy: %v", err)
	}
}
