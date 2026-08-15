package modelregistry

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"cyberagent-workbench/internal/llm"
)

const (
	// Qualification status taxonomy: the stable, redacted per-provider/model
	// endpoint classification shown to operators. Unknown means the status
	// has never been observed and is treated as not yet configured.
	QualificationStatusNotConfigured    = "not_configured"
	QualificationStatusAvailable        = "available"
	QualificationStatusProtocolMismatch = "protocol_mismatch"
	QualificationStatusAuthFailed       = "auth_failed"
	QualificationStatusNetworkFailed    = "network_failed"
	QualificationStatusRateLimit        = "rate_limit"
	QualificationStatusCapacity         = "capacity"
	QualificationStatusModelUnsupported = "model_unsupported"

	qualificationStatusSourceDiagnostic = "diagnostic"
	qualificationStatusSourceHarness    = "harness_qualification"
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
	Status    string `json:"status"`
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
}

func qualificationStatusSettingKey(provider, model string) string {
	return "qualification_status." + provider + "." + model
}

// PersistQualificationStatus stores the latest observed qualification status
// for one provider/model pair. Invalid or unbounded records are dropped.
func PersistQualificationStatus(ctx context.Context, writer RouteSettingWriter,
	provider, model, status, source string,
) {
	(&Registry{}).persistQualificationStatus(ctx, writer, provider, model, status, source)
}

func (r *Registry) persistQualificationStatus(ctx context.Context, writer RouteSettingWriter,
	provider, model, status, source string,
) {
	if r == nil || writer == nil || status == "" {
		return
	}
	record := persistedQualificationStatus{
		Status: status, Source: source, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(record)
	if err != nil || len(encoded) > 1024 {
		return
	}
	_ = writer.SetProviderSetting(ctx, qualificationStatusSettingKey(provider, model), string(encoded))
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
			if json.Unmarshal([]byte(value), &record) != nil || record.Status == "" ||
				!validQualificationStatus(record.Status) {
				continue
			}
			out[provider.Name+"."+model] = record
		}
	}
	return out
}

func validQualificationStatus(status string) bool {
	switch status {
	case QualificationStatusNotConfigured, QualificationStatusAvailable,
		QualificationStatusProtocolMismatch, QualificationStatusAuthFailed,
		QualificationStatusNetworkFailed, QualificationStatusRateLimit,
		QualificationStatusCapacity, QualificationStatusModelUnsupported:
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

