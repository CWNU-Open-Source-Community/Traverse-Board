// Command releasegate creates or reverifies the deterministic Standard Code
// Beta release-gate aggregate for one exact packaged candidate.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"cyberagent-workbench/internal/releasegate"
)

func main() {
	binary := flag.String("binary", "", "exact TraverseBoard.exe")
	archive := flag.String("archive", "", "exact portable ZIP")
	manifest := flag.String("portable-manifest", "", "portable-zip-manifest.json")
	metadata := flag.String("release-metadata", "", "release-metadata.json")
	bootstrap := flag.String("bootstrap", "", "standard-code-packaged-e2e.json")
	product := flag.String("product", "", "standard-code-product-e2e.json")
	security := flag.String("security", "", "standard-code-security-evidence.json")
	revision := flag.String("expected-revision", "", "exact candidate source revision")
	reportPath := flag.String("report", "", "new aggregate report path")
	verifyPath := flag.String("verify-report", "", "existing aggregate report to reverify")
	flag.Parse()
	values := []*string{binary, archive, manifest, metadata, bootstrap, product, security, revision}
	if flag.NArg() != 0 || anyBlank(values) ||
		(strings.TrimSpace(*reportPath) == "") == (strings.TrimSpace(*verifyPath) == "") {
		fail("exactly one of --report or --verify-report and all candidate/evidence flags are required")
	}
	report, err := releasegate.AggregateFiles(releasegate.InputPaths{
		BinaryPath: *binary, ArchivePath: *archive, PortableManifest: *manifest,
		ReleaseMetadata: *metadata, BootstrapReport: *bootstrap,
		ProductReport: *product, SecurityReport: *security,
		ExpectedRevision: *revision,
	})
	if err != nil {
		fail(err.Error())
	}
	if strings.TrimSpace(*verifyPath) != "" {
		err = releasegate.VerifyReport(*verifyPath, report)
	} else {
		err = releasegate.WriteReport(*reportPath, report)
	}
	if err != nil {
		fail(err.Error())
	}
	fmt.Fprintf(os.Stdout, "standard_code_release_gate: %s\n", report.Status)
	fmt.Fprintf(os.Stdout, "candidate_revision: %s\n", report.Candidate.Revision)
	fmt.Fprintf(os.Stdout, "candidate_binary_sha256: %s\n", report.Candidate.BinarySHA256)
	fmt.Fprintf(os.Stdout, "product_scenarios: %d\n", report.Coverage.ProductScenarios)
	fmt.Fprintf(os.Stdout, "security_backend_runs: %d\n", report.Coverage.SecurityPassedRuns)
	fmt.Fprintf(os.Stdout, "release_authorized: %t\n", report.Gate.ReleaseAuthorized)
	fmt.Fprintf(os.Stdout, "report_sha256: %s\n", report.ReportSHA256)
}

func anyBlank(values []*string) bool {
	for _, value := range values {
		if value == nil || strings.TrimSpace(*value) == "" {
			return true
		}
	}
	return false
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "releasegate:", message)
	os.Exit(1)
}
