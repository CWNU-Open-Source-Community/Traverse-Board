package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/toolcontract"
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
	packageID := "portable-" + portableIdentity(source.Kind+"\x00"+source.URI)
	pkg, err := plugins.CapturePortableDirectory(ctx, directory, packageID,
		toolcontract.SourceRef{URI: source.URI, Revision: source.Commit}, "")
	if err != nil {
		return ImportSkillFromSourceResult{}, err
	}
	result, err := s.registry.Import(ctx, ImportSkillPackageRequest{Raw: pkg.Archive(), Snapshot: &pkg.Snapshot,
		Source: source, EnableSkills: true, Surface: surface, OperationKey: operationKey,
		InstalledBy: actor, ConfirmUntrusted: confirmed})
	if err != nil {
		return ImportSkillFromSourceResult{}, err
	}
	return ImportSkillFromSourceResult{Portable: result.Installation}, nil
}
