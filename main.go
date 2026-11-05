// Copyright 2026 The acse-index Authors
// SPDX-License-Identifier: Apache-2.0

// Command acse-index reads `terraform show -json` (state, after apply) on
// stdin and writes a resource-index-1.0 document on stdout: per managed
// resource only its address, type, provider, module path and, for the cloud
// providers ACSE scans, its cloud id. It never writes an attribute value.
//
//	terraform show -json | acse-index --repo "$REPO" --dir "$DIR" \
//	    --commit "$SHA" --run "$RUN_REF" > index.json
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// version is the release version; the release workflow checks the tag
// against it, and the jq script carries the same value.
var version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now func() time.Time) int {
	fs := flag.NewFlagSet("acse-index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var h Header
	var showVersion bool
	fs.StringVar(&h.Repository, "repo", "", "repository as its code host names it, e.g. owner/name (required)")
	fs.StringVar(&h.WorkingDirectory, "dir", "", "repository-relative working directory of the root; . for the repository root (required)")
	fs.StringVar(&h.Commit, "commit", "", "full commit id the apply ran from (required)")
	fs.StringVar(&h.RunRef, "run", "", "reference of the apply run, e.g. a Terraform Cloud run id (required)")
	fs.StringVar(&h.ProducedAt, "produced-at", "", "UTC time to record, YYYY-MM-DDTHH:MM:SSZ (default: now)")
	fs.BoolVar(&showVersion, "version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if showVersion {
		fmt.Fprintf(stdout, "acse-index %s\n", version)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "acse-index: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	for _, req := range []struct{ name, v string }{{"repo", h.Repository}, {"dir", h.WorkingDirectory}, {"commit", h.Commit}, {"run", h.RunRef}} {
		if req.v == "" {
			fmt.Fprintf(stderr, "acse-index: --%s is required\n", req.name)
			return 2
		}
	}
	if h.ProducedAt == "" {
		h.ProducedAt = now().UTC().Format("2006-01-02T15:04:05Z")
	}
	if err := ValidateHeader(h); err != nil {
		fmt.Fprintf(stderr, "acse-index: %v\n", err)
		return 1
	}
	resources, err := Parse(bufio.NewReader(stdin))
	if err != nil {
		fmt.Fprintf(stderr, "acse-index: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(Render(h, version, resources)); err != nil {
		fmt.Fprintf(stderr, "acse-index: %v\n", err)
		return 1
	}
	return 0
}
