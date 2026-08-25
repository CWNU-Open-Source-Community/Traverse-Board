// Command packagede2e materializes the fixed issue #140 repositories and
// verifies their fail-then-repair oracle without granting product authority.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/packagede2e"
)

func main() {
	output := flag.String("output", "", "new directory that will receive the fixed repositories")
	reportPath := flag.String("report", "", "new path for the path-free fixture report")
	verifyToolchains := flag.Bool("verify-toolchains", false,
		"observe each baseline failure, apply its oracle repair in a disposable clone, and require a passing retry")
	flag.Parse()
	if flag.NArg() != 0 || strings.TrimSpace(*output) == "" ||
		strings.TrimSpace(*reportPath) == "" {
		fmt.Fprintln(os.Stderr, "packagede2e: --output and --report are required; positional arguments are not accepted")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "packagede2e:", err)
		os.Exit(1)
	}
	report, err := packagede2e.Prepare(ctx, packagede2e.PrepareOptions{
		OutputRoot: root, VerifyToolchains: *verifyToolchains})
	if err == nil {
		err = packagede2e.WriteReport(*reportPath, report)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "packagede2e:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "fixture_set_protocol: %s\n", report.ProtocolVersion)
	fmt.Fprintf(os.Stdout, "fixture_manifest_sha256: %s\n", report.ManifestSHA256)
	fmt.Fprintf(os.Stdout, "attack_matrix_sha256: %s\n", report.AttackMatrixSHA256)
	fmt.Fprintf(os.Stdout, "fixture_repositories: %d\n", report.RepositoryCount)
	fmt.Fprintf(os.Stdout, "required_attack_cases: %d\n", report.AttackCaseCount)
	fmt.Fprintf(os.Stdout, "fixture_oracle_verified: %t\n", report.OracleVerified)
}
