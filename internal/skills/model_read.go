package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"cyberagent-workbench/internal/redact"
)

// ReadForModel resolves an exact embedded version. Both the current policy and
// the pinned version must permit this delivery; archives cannot bypass a later
// invocation or mode restriction. Operator selections use their existing path.
func (r *Registry) ReadForModel(name, version, sourceSHA256 string, execution ExecutionContext) (ContextItem, error) {
	current, found := r.Get(name)
	if !found || !current.SupportsContext(execution) || !current.AllowsInvocation(InvocationSourceModel, false) {
		return ContextItem{}, errors.New("embedded Skill is unavailable for model invocation in this mode")
	}
	entry, found := r.version(name, version)
	if !found || entry.manifest.ContentSHA256 != sourceSHA256 ||
		!entry.manifest.SupportsContext(execution) || !entry.manifest.AllowsInvocation(InvocationSourceModel, false) {
		return ContextItem{}, errors.New("embedded Skill version, digest or invocation policy does not match")
	}
	redacted := redact.Text(string(entry.content))
	content := []byte(redacted.Text)
	if err := validateContextContent(content); err != nil {
		return ContextItem{}, err
	}
	if len(content) > entry.manifest.ContentTokenUpperBound {
		return ContextItem{}, errors.New("redacted embedded Skill exceeds its pinned bound")
	}
	digest := sha256.Sum256(content)
	count := 0
	for _, finding := range redacted.Findings {
		count += finding.Count
	}
	return ContextItem{Name: name, Version: version, SourceSHA256: sourceSHA256,
		SourceBytes: len(entry.content), SourceTokenUpperBound: entry.manifest.ContentTokenUpperBound,
		DeliveredSHA256: hex.EncodeToString(digest[:]), DeliveredBytes: len(content),
		TokenUpperBound: len(content), RedactionCount: count, Content: string(content)}, nil
}
