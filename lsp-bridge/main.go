package main

import (
	"log"
	"os"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/handler"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/proxy"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetOutput(os.Stderr)

	idx, err := index.New("")
	if err != nil {
		log.Fatalf("create index: %v", err)
	}
	defer idx.Close()

	h := handler.New(idx)

	p, err := proxy.New("atmos")
	if err != nil {
		log.Fatalf("create proxy: %v", err)
	}
	defer p.Close()

	if err := p.Run(os.Stdin, os.Stdout, h); err != nil {
		log.Fatalf("run proxy: %v", err)
	}
}
