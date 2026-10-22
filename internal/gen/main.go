// SPDX-License-Identifier: MIT

// Command ieee20305-gen generates Go types from the IEEE 2030.5 XSD schema.
//
// Usage:
//
//	go run ./internal/gen -schema <path-to-sep.xsd> -out . -pkg ieee20305
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/otfabric/go-ieee20305/internal/gen/emit"
	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func main() {
	schemaPath := flag.String("schema", "", "path to the IEEE 2030.5 XSD file (sep.xsd)")
	outDir := flag.String("out", ".", "output directory for generated files")
	pkgName := flag.String("pkg", "ieee20305", "Go package name for generated files")
	flag.Parse()

	if *schemaPath == "" {
		fmt.Fprintln(os.Stderr, "error: -schema flag is required")
		flag.Usage()
		os.Exit(1)
	}

	if err := run(*schemaPath, *outDir, *pkgName); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(schemaPath, outDir, pkgName string) error {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}

	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:])

	schema, err := xsd.Parse(data)
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}

	resolved, err := xsd.Resolve(schema)
	if err != nil {
		return fmt.Errorf("resolve schema: %w", err)
	}

	source := filepath.Base(schemaPath)
	e := emit.New(resolved, outDir, pkgName, source, hashHex)
	if err := e.Emit(); err != nil {
		return fmt.Errorf("emit: %w", err)
	}

	fmt.Printf("Generated %d complex types and %d simple types from %s\n",
		len(resolved.ComplexTypes), len(resolved.SimpleTypes), source)
	fmt.Printf("SHA-256: %s\n", hashHex)

	return nil
}
