//go:build clean_install_baseline_generate

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"cyberagent-workbench/internal/store"
)

func main() {
	sqlPath := flag.String("sql", "clean_install_baseline.sql", "generated schema SQL path")
	metadataPath := flag.String("metadata", "clean_install_baseline_generated.go", "generated metadata Go path")
	flag.Parse()
	if err := store.GenerateCleanInstallBaselineArtifacts(context.Background(), *sqlPath, *metadataPath); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
