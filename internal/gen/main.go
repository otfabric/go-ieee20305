// SPDX-License-Identifier: MIT

// Command ieee20305-gen generates Go types from the IEEE 2030.5 XSD schema.
//
// Usage:
//
//	go run ./internal/gen -schema <path-to-sep.xsd> -out . -pkg ieee20305
//	go run ./internal/gen -base <path-to-sep.xsd> -schema <path-to-csipaus.xsd> -out csipaus -pkg csipaus
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/otfabric/go-ieee20305/internal/gen/emit"
	"github.com/otfabric/go-ieee20305/internal/gen/wadl"
	"github.com/otfabric/go-ieee20305/internal/gen/xsd"
)

func main() {
	schemaPath := flag.String("schema", "", "path to the XSD file")
	basePath := flag.String("base", "", "path to the base XSD (for extension generation)")
	wadlPath := flag.String("wadl", "", "path to the WADL file (for resource table generation)")
	outDir := flag.String("out", ".", "output directory for generated files")
	pkgName := flag.String("pkg", "ieee20305", "Go package name for generated files")
	basePkg := flag.String("base-pkg", "github.com/otfabric/go-ieee20305", "Go import path of the base package")
	baseImport := flag.String("base-import", "ieee20305", "Go import alias for the base package")
	flag.Parse()

	if *schemaPath == "" {
		fmt.Fprintln(os.Stderr, "error: -schema flag is required")
		flag.Usage()
		os.Exit(1)
	}

	var err error
	if *wadlPath != "" {
		err = runWADL(*wadlPath, *outDir, *pkgName)
	} else if *basePath != "" {
		err = runExtension(*basePath, *schemaPath, *outDir, *pkgName, *basePkg, *baseImport)
	} else {
		err = run(*schemaPath, *outDir, *pkgName)
	}

	if err != nil {
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

func runWADL(wadlPath, outDir, pkgName string) error {
	data, err := os.ReadFile(wadlPath)
	if err != nil {
		return fmt.Errorf("read wadl: %w", err)
	}

	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:])

	app, err := wadl.Parse(data)
	if err != nil {
		return fmt.Errorf("parse wadl: %w", err)
	}

	source := filepath.Base(wadlPath)
	e := emit.NewResourceEmitter(app, outDir, pkgName, source, hashHex)
	if err := e.Emit(); err != nil {
		return fmt.Errorf("emit resources: %w", err)
	}

	flat := app.Flatten()
	fmt.Printf("Generated %d resources from %s\n", len(flat), source)
	fmt.Printf("SHA-256: %s\n", hashHex)

	return nil
}

func runExtension(basePath, extPath, outDir, pkgName, basePkg, baseImport string) error {
	extData, err := os.ReadFile(extPath)
	if err != nil {
		return fmt.Errorf("read extension: %w", err)
	}

	hash := sha256.Sum256(extData)
	hashHex := hex.EncodeToString(hash[:])

	eb, err := xsd.ParseExtension(basePath, extPath)
	if err != nil {
		return fmt.Errorf("parse extension: %w", err)
	}

	re, err := xsd.ResolveExtension(eb)
	if err != nil {
		return fmt.Errorf("resolve extension: %w", err)
	}

	source := filepath.Base(extPath)
	e := emit.NewExtension(re, outDir, pkgName, basePkg, baseImport, source, hashHex)
	if err := e.Emit(); err != nil {
		return fmt.Errorf("emit extension: %w", err)
	}

	extCount := 0
	for _, ct := range re.ComplexTypes {
		if !re.BaseTypeNames[ct.Name] {
			extCount++
		}
	}
	extSimple := 0
	for _, st := range re.SimpleTypes {
		if !re.BaseTypeNames[st.Name] {
			extSimple++
		}
	}

	fmt.Printf("Generated %d extension complex types and %d extension simple types from %s\n",
		extCount, extSimple, source)
	fmt.Printf("SHA-256: %s\n", hashHex)

	return nil
}
