package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/redact"
)

const maxRootActionJSONBytes = llm.MaxModelOutputBytes

const maxRootActionTrailingCommentaryBytes = 4 * 1024

type rootActionTrailingCommentaryRecovery struct {
	DiscardedTrailingBytes int
}

func parseRootAction(raw string) (domain.RootAction, error) {
	if len(raw) > maxRootActionJSONBytes {
		return domain.RootAction{}, apperror.New(apperror.CodeResourceExhausted, "provider root lifecycle action exceeds 65536 bytes")
	}
	if !utf8.ValidString(raw) {
		return domain.RootAction{}, apperror.New(apperror.CodeFailedPrecondition, "provider root lifecycle action is not valid UTF-8")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.RootAction{}, apperror.New(apperror.CodeFailedPrecondition, "provider returned an empty root lifecycle action")
	}
	if err := rejectDuplicateRootActionFields(raw); err != nil {
		return domain.RootAction{}, apperror.Wrap(apperror.CodeFailedPrecondition,
			"provider returned invalid root lifecycle JSON", err)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var action domain.RootAction
	if err := decoder.Decode(&action); err != nil {
		return domain.RootAction{}, apperror.Wrap(apperror.CodeFailedPrecondition, "provider returned invalid root lifecycle JSON", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values are not allowed")
		}
		return domain.RootAction{}, apperror.Wrap(apperror.CodeFailedPrecondition, "provider returned trailing root lifecycle data", err)
	}
	action.Version = strings.TrimSpace(action.Version)
	action.Message = strings.TrimSpace(action.Message)
	action.Summary = strings.TrimSpace(action.Summary)
	action.Reason = strings.TrimSpace(action.Reason)
	if err := action.Validate(); err != nil {
		return domain.RootAction{}, apperror.Wrap(apperror.CodeFailedPrecondition, "provider returned an invalid root lifecycle action", err)
	}
	return action, nil
}

// recoverRootActionWithTrailingCommentary is a deliberately narrow Provider
// compatibility path. parseRootAction remains the canonical strict parser;
// this helper is used only by RunSupervisor after that parser has rejected a
// response. It accepts one exact valid lifecycle object followed by bounded
// prose and never treats another JSON value or lifecycle marker as commentary.
func recoverRootActionWithTrailingCommentary(raw string) (
	domain.RootAction, rootActionTrailingCommentaryRecovery, bool,
) {
	if len(raw) > maxRootActionJSONBytes || !utf8.ValidString(raw) {
		return domain.RootAction{}, rootActionTrailingCommentaryRecovery{}, false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw[0] != '{' {
		return domain.RootAction{}, rootActionTrailingCommentaryRecovery{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		return domain.RootAction{}, rootActionTrailingCommentaryRecovery{}, false
	}
	offset := decoder.InputOffset()
	if offset <= 0 || offset >= int64(len(raw)) {
		return domain.RootAction{}, rootActionTrailingCommentaryRecovery{}, false
	}
	commentary := strings.TrimSpace(raw[offset:])
	if !validRootActionTrailingCommentary(commentary) {
		return domain.RootAction{}, rootActionTrailingCommentaryRecovery{}, false
	}
	action, err := parseRootAction(string(first))
	if err != nil || action.Version != domain.RootLifecycleVersion {
		return domain.RootAction{}, rootActionTrailingCommentaryRecovery{}, false
	}
	return action, rootActionTrailingCommentaryRecovery{
		DiscardedTrailingBytes: len(commentary),
	}, true
}

func validRootActionTrailingCommentary(commentary string) bool {
	if commentary == "" || len(commentary) > maxRootActionTrailingCommentaryBytes ||
		!utf8.ValidString(commentary) ||
		strings.Contains(strings.ToLower(commentary), "root_lifecycle") ||
		strings.ContainsAny(commentary, "{}[]") {
		return false
	}
	// A second scalar JSON value is just as ambiguous as another object or
	// array. Decode only the leading value; success means this is not prose.
	decoder := json.NewDecoder(strings.NewReader(commentary))
	var value any
	if err := decoder.Decode(&value); err == nil {
		return false
	}
	hasText := false
	for _, r := range commentary {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			hasText = true
		}
	}
	return hasText
}

func publicReplyRootAction(raw string) (domain.RootAction, bool) {
	if len(raw) > maxRootActionJSONBytes || !utf8.ValidString(raw) {
		return domain.RootAction{}, false
	}
	message := strings.TrimSpace(raw)
	if message == "" {
		return domain.RootAction{}, false
	}
	lower := strings.ToLower(message)
	if strings.HasPrefix(message, "{") || strings.HasPrefix(message, "[") ||
		strings.HasPrefix(message, `"`) || strings.HasPrefix(message, "```") ||
		strings.Contains(lower, domain.RootLifecycleVersion) {
		return domain.RootAction{}, false
	}
	action := domain.RootAction{
		Version: domain.RootLifecycleVersion,
		Kind:    domain.RootActionContinue,
		Message: message,
	}
	if err := action.Validate(); err != nil {
		return domain.RootAction{}, false
	}
	return action, true
}

func rejectDuplicateRootActionFields(raw string) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	opening, ok := token.(json.Delim)
	if !ok || opening != '{' {
		return errors.New("root lifecycle action must be a JSON object")
	}
	seen := map[string]struct{}{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("root lifecycle field name must be a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate root lifecycle field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return nil
}

func redactRootAction(action domain.RootAction) domain.RootAction {
	action.Message = redact.String(action.Message)
	action.Summary = redact.String(action.Summary)
	action.Reason = redact.String(action.Reason)
	return action
}

func rootActionPolicyText(action domain.RootAction) string {
	return strings.TrimSpace(strings.Join([]string{action.Message, action.Summary, action.Reason}, "\n"))
}
