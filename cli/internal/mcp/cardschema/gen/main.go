// Command gen writes internal/mcp/card_schemas.gen.go and
// internal/mcp/memory_schemas.gen.go from contracts/openapi.yaml
// (`go generate ./internal/mcp`).
package main

import (
	"flag"
	"log"
	"os"

	"github.com/ingki3/agent-collabortion/cli/internal/mcp/cardschema"
)

func main() {
	spec := flag.String("spec", "../../../contracts/openapi.yaml", "openapi.yaml")
	out := flag.String("out", "card_schemas.gen.go", "output file (card tools)")
	memOut := flag.String("memory-out", "", "output file (mission ledger tools); empty = not written")
	flag.Parse()
	b, err := os.ReadFile(*spec)
	if err != nil {
		log.Fatal(err)
	}
	src, err := cardschema.Generate(b)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		log.Fatal(err)
	}
	if *memOut != "" {
		msrc, err := cardschema.GenerateMemory(b)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*memOut, msrc, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
