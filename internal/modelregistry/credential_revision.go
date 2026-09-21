package modelregistry

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/llm"
)

const credentialRevisionSettingPrefix = "credential_revision."

func credentialRevisionSettingKey(provider string) string {
	return credentialRevisionSettingPrefix + provider
}

func (r *Registry) loadCredentialRevisions(ctx context.Context,
	reader RouteSettingReader,
) (map[string]uint64, error) {
	out := make(map[string]uint64)
	if r == nil || reader == nil {
		return out, nil
	}
	r.mu.RLock()
	providers := make([]ProviderAvailability, len(r.providers))
	copy(providers, r.providers)
	r.mu.RUnlock()
	for _, provider := range providers {
		value, found, err := reader.GetProviderSetting(ctx,
			credentialRevisionSettingKey(provider.Name))
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		revision, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err == nil && revision > 0 {
			out[provider.Name] = revision
		}
	}
	return out, nil
}

func (r *Registry) credentialRevision(provider string) uint64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.credentialRevisions[provider]
}

// MutateProviderCredential serializes an OS credential mutation with Harness
// qualification. The durable revision is advanced before the external store is
// touched, so both a successful change and an ambiguous/failed change revoke
// qualifications obtained with the previous credential. Reload runs before the
// method returns; current memory is failed closed even when reload cannot finish.
func (r *Registry) MutateProviderCredential(ctx context.Context,
	settings RouteSettingStore, provider string, mutation func() error,
) (ReloadResult, error) {
	if r == nil || r.router == nil || ctx == nil || settings == nil || mutation == nil {
		return ReloadResult{}, errors.New("Provider credential mutation dependencies are required")
	}
	provider = strings.TrimSpace(provider)
	if !validAvailabilityIdentifier(provider, maxPublicProviderNameBytes) {
		return ReloadResult{}, errors.New("Provider credential mutation target is invalid")
	}
	if err := ctx.Err(); err != nil {
		return ReloadResult{}, err
	}
	r.qualificationMu.Lock()
	defer r.qualificationMu.Unlock()
	r.routeMu.Lock()
	defer r.routeMu.Unlock()

	current := r.credentialRevision(provider)
	if current == ^uint64(0) {
		return ReloadResult{}, errors.New("Provider credential revision is exhausted")
	}
	next := current + 1
	if err := settings.SetProviderSetting(ctx, credentialRevisionSettingKey(provider),
		strconv.FormatUint(next, 10)); err != nil {
		return ReloadResult{}, fmt.Errorf("persist Provider credential revision: %w", err)
	}
	r.invalidateProviderQualificationMemory(provider, next)
	mutationErr := mutation()
	reload, reloadErr := r.reloadLocked(ctx, settings)
	if mutationErr != nil {
		if reloadErr != nil {
			return reload, fmt.Errorf("Provider credential mutation failed and Registry reload failed: %v: %w",
				mutationErr, reloadErr)
		}
		return reload, mutationErr
	}
	if reloadErr != nil {
		return reload, reloadErr
	}
	return reload, nil
}

func (r *Registry) invalidateProviderQualificationMemory(provider string, revision uint64) {
	r.mu.Lock()
	if r.credentialRevisions == nil {
		r.credentialRevisions = make(map[string]uint64)
	}
	r.credentialRevisions[provider] = revision
	if r.qualificationStatuses == nil {
		r.qualificationStatuses = make(map[string]persistedQualificationStatus)
	}
	models := make([]string, 0)
	for _, current := range r.providers {
		if current.Name != provider {
			continue
		}
		models = append(models, current.Models...)
		for _, model := range current.Models {
			r.qualificationStatuses[provider+"."+model] = persistedQualificationStatus{
				Status: QualificationStatusNotConfigured, Source: qualificationStatusSourceAvailability,
				CredentialRevision: revision,
			}
		}
		break
	}
	r.mu.Unlock()
	for _, model := range models {
		r.router.ClearHarnessQualification(llm.ModelRef{Provider: provider, Model: model})
	}
}
