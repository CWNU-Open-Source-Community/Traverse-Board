package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/plugins"
)

func portableIdentity(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *SkillCatalogService) importPortableDirectory(ctx context.Context, directory string,
	source plugins.InstallSource, surface domain.ExecutionSurface, operationKey, actor string, confirmed bool,
) (ImportSkillFromSourceResult, error) {
	actor, operationKey = strings.TrimSpace(actor), strings.TrimSpace(operationKey)
	if !confirmed {
		return ImportSkillFromSourceResult{}, apperror.New(apperror.CodePolicyDenied, "portable Skill instructions require explicit untrusted confirmation")
	}
	if !surface.Valid() || operationKey == "" || len(operationKey) > 512 || actor == "" {
		return ImportSkillFromSourceResult{}, apperror.New(apperror.CodeInvalidArgument, "portable import requires surface, actor and a bounded operation key")
	}
	store, ok := s.store.(plugins.Store)
	if !ok {
		return ImportSkillFromSourceResult{}, apperror.New(apperror.CodeFailedPrecondition, "the existing plugin installation store is unavailable")
	}
	service, err := plugins.NewService(store)
	if err != nil {
		return ImportSkillFromSourceResult{}, err
	}
	source.Surface = string(surface)
	source.OperationKeyDigest = portableIdentity("skill-directory-import\x00" + actor + "\x00" + operationKey)
	packageID := "portable-" + portableIdentity(source.Kind+"\x00"+source.URI)
	installed, _, err := service.StageDirectory(ctx, directory, packageID, source, "", actor, "")
	if err != nil {
		return ImportSkillFromSourceResult{}, apperror.Normalize(err)
	}
	// The caller confirmed instruction installation, not script/MCP execution.
	// Resume an interrupted staged/approved import; never renew a disabled,
	// revoked, quarantined or rolled-back installation by importing it again.
	for _, action := range []plugins.ReviewAction{plugins.ReviewApprove, plugins.ReviewEnable} {
		if (action == plugins.ReviewApprove && installed.State != plugins.StateStaged) ||
			(action == plugins.ReviewEnable && installed.State != plugins.StateApproved) {
			continue
		}
		if !slices.Contains(installed.Capabilities(), plugins.CapabilitySkills) {
			break
		}
		installed, err = service.Review(ctx, installed.ID, plugins.ReviewRequest{Action: action,
			ExpectedPackageFingerprint: installed.PackageFingerprint, ExpectedGeneration: installed.Generation,
			Capabilities: []plugins.Capability{plugins.CapabilitySkills}, ConfirmUntrusted: true, ReviewedBy: actor})
		if err != nil {
			return ImportSkillFromSourceResult{}, err
		}
	}
	return ImportSkillFromSourceResult{Portable: &installed}, nil
}
