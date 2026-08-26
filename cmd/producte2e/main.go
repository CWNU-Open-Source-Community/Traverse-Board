// Command producte2e produces the fail-closed issue #182 product evidence
// report from an exact packaged candidate and its durable Standard Code facts.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"cyberagent-workbench/internal/producte2e"
)

func main() {
	binary := flag.String("binary", "", "exact extracted TraverseBoard.exe")
	zipPath := flag.String("zip", "", "exact portable ZIP")
	manifest := flag.String("portable-manifest", "", "portable-zip-manifest.json")
	metadata := flag.String("release-metadata", "", "release-metadata.json")
	fixtureReport := flag.String("fixture-report", "", "real four-language oracle report")
	home := flag.String("home", "", "stopped candidate's isolated CYBERAGENT_HOME")
	evidenceRoot := flag.String("evidence-root", "", "directory containing hash-bound UI and surface captures")
	runbookPath := flag.String("runbook", "", "candidate-bound product runbook evidence")
	reportPath := flag.String("report", "", "new path for the path-free product report")
	expectedRevision := flag.String("expected-revision", "", "exact 40/64-hex source revision")
	flag.Parse()
	values := []*string{binary, zipPath, manifest, metadata, fixtureReport, home, evidenceRoot,
		runbookPath, reportPath, expectedRevision}
	if flag.NArg() != 0 || anyBlank(values) {
		fmt.Fprintln(os.Stderr, "producte2e: --binary, --zip, --portable-manifest, --release-metadata, --fixture-report, --home, --evidence-root, --runbook, --report, and --expected-revision are required; positional arguments are not accepted")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	runbookInfo, err := os.Lstat(*runbookPath)
	if err != nil || !runbookInfo.Mode().IsRegular() ||
		runbookInfo.Mode()&os.ModeSymlink != 0 || runbookInfo.Size() <= 0 ||
		runbookInfo.Size() > 2*1024*1024 {
		fail(fmt.Errorf("runbook is unavailable, unsafe, or exceeds 2 MiB"))
	}
	runbookBytes, err := os.ReadFile(*runbookPath)
	if err != nil {
		fail(err)
	}
	runbook, err := producte2e.DecodeRunbook(runbookBytes)
	if err != nil {
		fail(err)
	}
	candidate, fixture, err := producte2e.ValidateCandidate(producte2e.CandidateOptions{
		BinaryPath: *binary, ZipPath: *zipPath, PortableManifestPath: *manifest,
		ReleaseMetadataPath: *metadata, FixtureReportPath: *fixtureReport,
		ExpectedRevision: *expectedRevision,
	})
	if err != nil {
		fail(err)
	}
	report, err := producte2e.Produce(ctx, producte2e.ProduceOptions{
		Home: *home, EvidenceRoot: *evidenceRoot, Candidate: candidate,
		Fixture: fixture, Runbook: runbook,
		RunbookSHA256: producte2e.RunbookDigest(runbookBytes),
	})
	if err == nil {
		err = producte2e.WriteReport(*reportPath, report)
	}
	if err != nil {
		fail(err)
	}
	fmt.Fprintf(os.Stdout, "standard_code_product_e2e: %s\n", report.Status)
	fmt.Fprintf(os.Stdout, "candidate_revision: %s\n", report.Candidate.Revision)
	fmt.Fprintf(os.Stdout, "candidate_binary_sha256: %s\n", report.Candidate.BinarySHA256)
	fmt.Fprintf(os.Stdout, "scenario_count: %d\n", len(report.Scenarios))
	fmt.Fprintf(os.Stdout, "real_process_jobs: %d\n", report.Coverage.RealProcessJobs)
	fmt.Fprintf(os.Stdout, "evidence_sha256: %s\n", report.EvidenceSHA256)
}

func anyBlank(values []*string) bool {
	for _, value := range values {
		if value == nil || strings.TrimSpace(*value) == "" {
			return true
		}
	}
	return false
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "producte2e:", err)
	os.Exit(1)
}
