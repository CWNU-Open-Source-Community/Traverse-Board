package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/llm"
)

const (
	// Qualification status taxonomy: the stable, redacted per-provider/model
	// endpoint classification shown to operators. Unknown means the status
	// has never been observed and is treated as not yet configured.
	QualificationStatusNotConfigured      = "not_configured"
	QualificationStatusAvailable          = "available"
	QualificationStatusProtocolMismatch   = "protocol_mismatch"
	QualificationStatusAuthFailed         = "auth_failed"
	QualificationStatusNetworkFailed      = "network_failed"
	QualificationStatusRateLimit          = "rate_limit"
	QualificationStatusCapacity           = "capacity"
	QualificationStatusModelUnsupported   = "model_unsupported"
	QualificationStatusResponseIncomplete = "response_incomplete"

	qualificationStatusSourceDiagnostic   = "diagnostic"
	qualificationStatusSourceHarness      = "harness_qualification"
	qualificationStatusSourceAvailability = "availability"
)

// QualificationStatusFor maps an observed outcome onto the closed taxonomy.
// A successful observation is available; every failure reason folds onto its
// stable status; the absence of any observation is not_configured.
func QualificationStatusFor(outcome llm.Outcome, reason llm.ProviderFailureReason) string {
	if outcome == llm.OutcomeSuccess {
		return QualificationStatusAvailable
	}
	switch reason {
	case llm.ProviderFailureContextLimit, llm.ProviderFailureOutputLimit, llm.ProviderFailurePaused, llm.ProviderFailureRefusal:
		return QualificationStatusResponseIncomplete
	case llm.ProviderFailureAuthentication:
		return QualificationStatusAuthFailed
	case llm.ProviderFailureNetwork:
		return QualificationStatusNetworkFailed
	case llm.ProviderFailureRateLimit:
		return QualificationStatusRateLimit
	case llm.ProviderFailureCapacity:
		return QualificationStatusCapacity
	case llm.ProviderFailureModelNotFound:
		return QualificationStatusModelUnsupported
	case llm.ProviderFailureProtocolIncompatible:
		return QualificationStatusProtocolMismatch
	case llm.ProviderFailureNotConfigured:
		return QualificationStatusNotConfigured
	default:
		return QualificationStatusNotConfigured
	}
}

// persistedQualificationStatus is the durable projection of the latest
// observation for one provider/model pair.
type persistedQualificationStatus struct {
	Status             string `json:"status"`
	Source             string `json:"source"`
	CheckedAt          string `json:"checked_at"`
	CredentialRevision uint64 `json:"credential_revision"`
	DefinitionRevision uint64 `json:"definition_revision"`
	BindingDigest      string `json:"binding_digest"`
}

func qualificationStatusSettingKey(provider, model string) string {
	return "qualification_status." + provider + "." + model
}

// PersistQualificationStatus stores the latest observed qualification status
// for one provider/model pair. Invalid or unbounded records are dropped.
func PersistQualificationStatus(ctx context.Context, writer RouteSettingWriter,
	provider, model, status, source string,
) error {
	return (&Registry{}).persistQualificationStatus(ctx, writer, provider, model, status, source)
}

// RecordQualificationStatus durably publishes one observation and updates the
// live catalog projection only after persistence succeeds.
func (r *Registry) RecordQualificationStatus(ctx context.Context,
	writer RouteSettingWriter, provider, model, status, source string,
) error {
	if r == nil {
		return errors.New("qualification status persistence dependencies are required")
	}
	r.qualificationMu.Lock()
	defer r.qualificationMu.Unlock()
	return r.persistQualificationStatus(ctx, writer, provider, model, status, source)
}

func (r *Registry) persistQualificationStatus(ctx context.Context, writer RouteSettingWriter,
	provider, model, status, source string,
) error {
	if r == nil || writer == nil || status == "" {
		return errors.New("qualification status persistence dependencies are required")
	}
	if !validQualificationStatus(status) {
		return errors.New("qualification status is invalid")
	}
	definitionRevision, bindingDigest, bindingFound := r.qualificationBinding(provider, model)
	if status != QualificationStatusNotConfigured && !bindingFound {
		return errors.New("qualification status Provider binding is unavailable")
	}
	record := persistedQualificationStatus{
		Status: status, Source: normalizeQualificationStatusSource(source),
		CheckedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		CredentialRevision: r.credentialRevision(provider),
		DefinitionRevision: definitionRevision,
		BindingDigest:      bindingDigest,
	}
	encoded, err := json.Marshal(record)
	if err != nil || len(encoded) > 1024 {
		return errors.New("qualification status record is invalid")
	}
	if err := writer.SetProviderSetting(ctx, qualificationStatusSettingKey(provider, model), string(encoded)); err != nil {
		return err
	}
	r.mu.Lock()
	if r.qualificationStatuses == nil {
		r.qualificationStatuses = make(map[string]persistedQualificationStatus)
	}
	r.qualificationStatuses[provider+"."+model] = record
	r.mu.Unlock()
	return nil
}

// loadQualificationStatuses reads the durable latest status per model.
func (r *Registry) loadQualificationStatuses(ctx context.Context, reader RouteSettingReader) map[string]persistedQualificationStatus {
	out := make(map[string]persistedQualificationStatus)
	if r == nil || reader == nil {
		return out
	}
	r.mu.RLock()
	providers := make([]ProviderAvailability, len(r.providers))
	copy(providers, r.providers)
	r.mu.RUnlock()
	for _, provider := range providers {
		for _, model := range provider.Models {
			value, found, err := reader.GetProviderSetting(ctx,
				qualificationStatusSettingKey(provider.Name, model))
			if err != nil || !found {
				continue
			}
			var record persistedQualificationStatus
			definitionRevision, bindingDigest, bindingFound := r.qualificationBinding(
				provider.Name, model)
			if json.Unmarshal([]byte(value), &record) != nil || record.Status == "" ||
				!validQualificationStatus(record.Status) ||
				record.CredentialRevision != r.credentialRevision(provider.Name) ||
				record.DefinitionRevision != definitionRevision ||
				(record.Status != QualificationStatusNotConfigured &&
					(!bindingFound || record.BindingDigest == "" ||
						record.BindingDigest != bindingDigest)) {
				continue
			}
			out[provider.Name+"."+model] = record
		}
	}
	return out
}

func (r *Registry) qualificationBinding(provider, model string) (uint64, string, bool) {
	if r == nil || r.router == nil {
		return 0, "", false
	}
	var definitionRevision uint64
	found := false
	r.mu.RLock()
	for _, current := range r.providers {
		if current.Name != provider {
			continue
		}
		for _, candidate := range current.Models {
			if candidate == model {
				definitionRevision = current.DefinitionRevision
				found = true
				break
			}
		}
		break
	}
	r.mu.RUnlock()
	if !found {
		return 0, "", false
	}
	profile, err := r.router.HarnessProfile(llm.ModelRef{Provider: provider, Model: model})
	if err != nil {
		return definitionRevision, "", false
	}
	return definitionRevision, profile.BindingDigest, true
}

func validQualificationStatus(status string) bool {
	switch status {
	case QualificationStatusNotConfigured, QualificationStatusAvailable,
		QualificationStatusProtocolMismatch, QualificationStatusAuthFailed,
		QualificationStatusNetworkFailed, QualificationStatusRateLimit,
		QualificationStatusCapacity, QualificationStatusModelUnsupported, QualificationStatusResponseIncomplete:
		return true
	default:
		return false
	}
}

// normalizeQualificationStatusSource folds an arbitrary source label onto the
// closed set used by the public projection.
func normalizeQualificationStatusSource(source string) string {
	source = strings.TrimSpace(source)
	switch source {
	case qualificationStatusSourceDiagnostic, qualificationStatusSourceHarness,
		qualificationStatusSourceAvailability:
		return source
	default:
		return qualificationStatusSourceAvailability
	}
}
