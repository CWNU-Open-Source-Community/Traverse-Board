package plugins

import (
	"context"
	"cyberagent-workbench/internal/apperror"
)

type History struct {
	InstallationID              string
	PackageID                   string
	Installations               []Installation
	Publisher                   *PublisherTrust
	PublisherInstallationIDs    []string
	TotalVersions               int
	TotalPublisherInstallations int
}

func (s *Service) History(ctx context.Context, installationID string) (History, error) {
	current, err := s.store.GetPluginInstallation(ctx, installationID)
	if err != nil {
		return History{}, err
	}
	result := History{InstallationID: current.ID, PackageID: current.PackageID(), Installations: []Installation{}, PublisherInstallationIDs: []string{}}
	reader, ok := s.store.(interface {
		ListPluginVersions(context.Context, string, string, string, int) ([]Installation, int, error)
		ListPluginPublisherInstallations(context.Context, string, int) ([]Installation, int, error)
	})
	if !ok {
		return History{}, apperror.New(apperror.CodeUnavailable, "complete plugin history counts are unavailable")
	}
	result.Installations, result.TotalVersions, err = reader.ListPluginVersions(ctx, current.PackageID(), current.ProtocolVersion, current.Source.Surface, 1000)
	if err != nil {
		return History{}, err
	}
	// An explicitly selected retained installation remains present even when it
	// predates the bounded latest-version window.
	selected := false
	for _, value := range result.Installations {
		if value.ID == current.ID {
			selected = true
			break
		}
	}
	if !selected {
		if len(result.Installations) == 1000 {
			result.Installations = result.Installations[:999]
		}
		result.Installations = append(result.Installations, current)
	}
	if current.SignatureValid {
		trust, found, err := s.store.GetPluginPublisherTrust(ctx, current.PublisherFingerprint)
		if err != nil {
			return History{}, err
		}
		if found && trust.Publisher == current.Manifest.Publisher && trust.PublicKey == current.PublisherPublicKey {
			result.Publisher = &trust
			all, total, err := reader.ListPluginPublisherInstallations(ctx, trust.Fingerprint, 1000)
			if err != nil {
				return History{}, err
			}
			result.TotalPublisherInstallations = total
			for _, value := range all {
				if value.PublisherFingerprint == trust.Fingerprint && value.State != StateRevoked && value.State != StateRolledBack {
					result.PublisherInstallationIDs = append(result.PublisherInstallationIDs, value.ID)
				}
			}
		}
	}
	return result, nil
}

func (s *Service) RevokeInstallationPublisher(ctx context.Context, installationID, expectedFingerprint string, expectedGeneration int64, actor string) (PublisherTrust, error) {
	installation, err := s.store.GetPluginInstallation(ctx, installationID)
	if err != nil {
		return PublisherTrust{}, err
	}
	if !installation.SignatureValid || installation.PublisherFingerprint != expectedFingerprint {
		return PublisherTrust{}, apperror.New(apperror.CodeConflict, "publisher revocation differs from the selected signed installation")
	}
	return s.RevokePublisher(ctx, expectedFingerprint, expectedGeneration, actor)
}
