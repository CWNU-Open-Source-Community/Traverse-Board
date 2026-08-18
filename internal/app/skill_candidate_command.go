package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/skills"
)

func (a *App) skillCandidateService(registry *skills.Registry) (*application.SkillCandidateService, error) {
	if err := a.ensureStore(); err != nil {
		return nil, err
	}
	objects, err := skills.NewLocalPackageObjectStore(a.home)
	if err != nil {
		return nil, err
	}
	packages := application.NewSkillPackageRegistryService(a.store, objects, registry)
	return application.NewSkillCandidateService(a.store, packages), nil
}

func (a *App) skillCandidateCommand(ctx context.Context, args []string,
	registry *skills.Registry,
) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent skill candidate list|show|approve|reject|import")
	}
	service, err := a.skillCandidateService(registry)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		flags := newFlagSet("skill candidate list", a.errOut)
		runID := flags.String("run", "", "filter by Run id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("usage: cyberagent skill candidate list [--run <run-id>]")
		}
		values, err := service.List(ctx, *runID)
		if err != nil {
			return err
		}
		for _, value := range values {
			fmt.Fprintf(a.out, "%s\t%s@%s\tstatus=%s\tfingerprint=%s\tpackage=%s\trun=%s\tcreated=%s\n",
				value.Candidate.ID, value.Candidate.Manifest.Name,
				value.Candidate.Manifest.Version, value.Status(),
				value.Candidate.CandidateFingerprint, value.Candidate.PackageFingerprint,
				value.Candidate.RunID, value.Candidate.CreatedAt.Format(time.RFC3339Nano))
		}
		fmt.Fprintf(a.out, "candidate_count: %d\n", len(values))
		return nil
	case "show":
		flags := newFlagSet("skill candidate show", a.errOut)
		showContent := flags.Bool("show-content", false,
			"display the inert untrusted body for human review")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
			"show-content": false,
		})); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("usage: cyberagent skill candidate show <candidate-id> [--show-content]")
		}
		value, err := service.Get(ctx, flags.Arg(0))
		if err != nil {
			return err
		}
		printSkillCandidate(a, value, *showContent)
		return nil
	case "approve", "reject":
		flags := newFlagSet("skill candidate "+args[0], a.errOut)
		fingerprint := flags.String("candidate-fingerprint", "",
			"exact fingerprint shown during review")
		operationKey := flags.String("operation-key", "", "stable idempotency key")
		operator := flags.String("operator", "cli_operator", "human reviewer identity")
		reason := flags.String("reason", "", "review rationale; required for rejection")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
			"candidate-fingerprint": true, "operation-key": true,
			"operator": true, "reason": true,
		})); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return fmt.Errorf("usage: cyberagent skill candidate %s <candidate-id> --candidate-fingerprint <sha256> --operation-key <stable-key> [--operator cli_operator] [--reason text]", args[0])
		}
		decision := skills.SkillCandidateReviewApprove
		if args[0] == "reject" {
			decision = skills.SkillCandidateReviewReject
		}
		result, err := service.Review(ctx, application.ReviewSkillCandidateRequest{
			CandidateID: flags.Arg(0), CandidateFingerprint: *fingerprint,
			Decision: decision, Reason: *reason, OperationKey: *operationKey,
			Reviewer: *operator,
		})
		if err != nil {
			return err
		}
		printSkillCandidate(a, result.Record, false)
		fmt.Fprintf(a.out, "replayed: %t\n", result.Replayed)
		return nil
	case "import":
		flags := newFlagSet("skill candidate import", a.errOut)
		fingerprint := flags.String("candidate-fingerprint", "",
			"exact fingerprint approved by a human")
		operationKey := flags.String("operation-key", "", "stable idempotency key")
		operator := flags.String("operator", "cli_operator", "importing operator identity")
		confirmed := flags.Bool("confirm-untrusted-skill", false,
			"separately confirm importing untrusted instructions")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
			"candidate-fingerprint": true, "operation-key": true,
			"operator": true, "confirm-untrusted-skill": false,
		})); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("usage: cyberagent skill candidate import <candidate-id> --candidate-fingerprint <sha256> --operation-key <stable-key> --confirm-untrusted-skill [--operator cli_operator]")
		}
		result, err := service.Import(ctx, application.ImportSkillCandidateRequest{
			CandidateID: flags.Arg(0), CandidateFingerprint: *fingerprint,
			OperationKey: *operationKey, ImportedBy: *operator,
			ConfirmUntrusted: *confirmed,
		})
		if err != nil {
			return err
		}
		printSkillCandidate(a, result.Record, false)
		printInstalledSkillPackage(a, result.InstalledPackage)
		fmt.Fprintf(a.out, "replayed: %t\nrecovered_pending: %t\n",
			result.Replayed, result.RecoveredPending)
		return nil
	default:
		return fmt.Errorf("unknown skill candidate subcommand %q", args[0])
	}
}

func printSkillCandidate(a *App, value skills.SkillCandidateRecord, showContent bool) {
	candidate := value.Candidate
	fmt.Fprintf(a.out, "candidate_id: %s\nprotocol: %s\nstatus: %s\nskill: %s\nsurface: %s\nprofiles: %s\nsurfaces: %s\nphases: %s\nroles: %s\nuser_invocable: %t\nmodel_invocable: %t\nexplicit_only: %t\ntool_dependencies: %s\ncontent_sha256: %s\ncontent_bytes: %d\narchive_sha256: %s\npackage_fingerprint: %s\ncandidate_fingerprint: %s\ninvocation_id: %s\nrun_id: %s\nrequested_by: %s\ncreated_at: %s\nhuman_review_required: %t\ninstallation_authorized: %t\nselection_authorized: false\n",
		candidate.ID, candidate.ProtocolVersion, value.Status(), candidate.Manifest.Name,
		candidate.Surface, joinProfiles(candidate.Manifest.Profiles),
		joinSurfaces(candidate.Manifest.Surfaces), joinPhases(candidate.Manifest.Phases),
		joinRoles(candidate.Manifest.Roles), candidate.Manifest.UserInvocable,
		candidate.Manifest.ModelInvocable, candidate.Manifest.ExplicitOnly,
		joinToolDependencies(candidate.Manifest.ToolDependencies),
		candidate.Manifest.ContentSHA256, candidate.Manifest.ContentBytes,
		candidate.ArchiveSHA256, candidate.PackageFingerprint,
		candidate.CandidateFingerprint, candidate.InvocationID, candidate.RunID,
		candidate.RequestedBy,
		candidate.CreatedAt.Format(time.RFC3339Nano), value.Review == nil,
		value.Import != nil)
	if value.Review != nil {
		fmt.Fprintf(a.out, "review_decision: %s\nreview_fingerprint: %s\nreviewer: %s\nreview_reason: %s\nreviewed_at: %s\n",
			value.Review.Decision, value.Review.ReviewFingerprint, value.Review.Reviewer,
			value.Review.Reason, value.Review.CreatedAt.Format(time.RFC3339Nano))
	}
	if value.Import != nil {
		fmt.Fprintf(a.out, "import_id: %s\nimport_fingerprint: %s\ninstallation_id: %s\ninstallation_fingerprint: %s\nimported_by: %s\nimported_at: %s\n",
			value.Import.ID, value.Import.ImportFingerprint, value.Import.InstallationID,
			value.Import.InstallationFingerprint, value.Import.ImportedBy,
			value.Import.CreatedAt.Format(time.RFC3339Nano))
	}
	if showContent {
		fmt.Fprintln(a.out, "content_trust: untrusted_inert_candidate")
		fmt.Fprintln(a.out, "----- BEGIN UNTRUSTED SKILL CANDIDATE -----")
		fmt.Fprint(a.out, candidate.Content)
		if candidate.Content != "" && candidate.Content[len(candidate.Content)-1] != '\n' {
			fmt.Fprintln(a.out)
		}
		fmt.Fprintln(a.out, "----- END UNTRUSTED SKILL CANDIDATE -----")
	} else {
		fmt.Fprintln(a.out, "content_body_exposed: false")
	}
}
