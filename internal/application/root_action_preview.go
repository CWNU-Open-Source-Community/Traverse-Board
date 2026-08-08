package application

import (
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/redact"
)

const rootPreviewTailRunes = 64

type rootMessagePreview struct {
	Text     string
	Ready    bool
	Complete bool
}

type rootMessagePreviewer struct {
	checker  policy.Checker
	lastText string
	complete bool
}

func newRootMessagePreviewer(checker policy.Checker) *rootMessagePreviewer {
	return &rootMessagePreviewer{checker: checker}
}

func (p *rootMessagePreviewer) Update(raw string) (string, bool, bool) {
	preview, err := extractRootMessagePreview(raw)
	text := ""
	complete := false
	if err == nil && preview.Ready {
		text, complete = p.safeText(preview)
	}
	changed := text != p.lastText || complete != p.complete
	p.lastText = text
	p.complete = complete
	return text, complete, changed
}

func (p *rootMessagePreviewer) safeText(preview rootMessagePreview) (string, bool) {
	value := strings.TrimSpace(preview.Text)
	if value == "" {
		return "", preview.Complete
	}
	if !preview.Complete {
		value = truncateStreamingSecret(value)
		value = withholdPreviewTail(value, rootPreviewTailRunes)
	}
	if value == "" {
		return "", preview.Complete
	}
	if p.checker != nil {
		decision := p.checker.CheckText("supervisor_assistant_preview", value)
		if !decision.Allowed {
			return "", false
		}
	}
	return redact.String(value), preview.Complete
}

func truncateStreamingSecret(value string) string {
	lower := strings.ToLower(value)
	markers := []string{
		"-----begin ", "authorization:", "bearer ", "api_key=", "api_key:",
		"api-key=", "api-key:", "apikey=", "apikey:", "token=", "token:",
		"secret=", "secret:", "password=", "password:", "passwd=", "passwd:",
		"private_key=", "private_key:", "private-key=", "private-key:",
		"github_pat_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "sk-", "tp-",
	}
	cut := len(value)
	for _, marker := range markers {
		if index := strings.Index(lower, marker); index >= 0 && index < cut {
			cut = index
		}
	}
	return strings.TrimSpace(value[:cut])
}

func withholdPreviewTail(value string, count int) string {
	runes := []rune(value)
	if count <= 0 || len(runes) <= count {
		return ""
	}
	return strings.TrimSpace(string(runes[:len(runes)-count]))
}

// extractRootMessagePreview only recognizes string fields in the root lifecycle
// object. It intentionally refuses Markdown fences, nested message keys, unknown
// fields, and duplicate fields.
func extractRootMessagePreview(raw string) (rootMessagePreview, error) {
	if len(raw) > maxRootActionJSONBytes {
		return rootMessagePreview{}, errors.New("root lifecycle preview exceeds its byte limit")
	}
	position := skipJSONSpace(raw, 0)
	if position == len(raw) {
		return rootMessagePreview{}, nil
	}
	if raw[position] != '{' {
		return rootMessagePreview{}, errors.New("root lifecycle preview must start with an object")
	}
	position++
	seen := map[string]struct{}{}
	version := ""
	action := ""
	message := ""
	messageSeen := false
	messageComplete := false
	first := true

	current := func() rootMessagePreview {
		ready := messageSeen && version == domain.RootLifecycleVersion && validRootPreviewAction(action)
		return rootMessagePreview{Text: message, Ready: ready, Complete: ready && messageComplete}
	}

	for {
		position = skipJSONSpace(raw, position)
		if position == len(raw) {
			return current(), nil
		}
		if raw[position] == '}' {
			return current(), nil
		}
		if !first {
			if raw[position] != ',' {
				return rootMessagePreview{}, errors.New("root lifecycle fields must be comma separated")
			}
			position = skipJSONSpace(raw, position+1)
			if position == len(raw) {
				return current(), nil
			}
		}
		first = false

		key, next, complete, err := parseJSONStringPrefix(raw, position)
		if err != nil {
			return rootMessagePreview{}, err
		}
		if !complete {
			return current(), nil
		}
		if _, duplicate := seen[key]; duplicate {
			return rootMessagePreview{}, errors.New("root lifecycle preview contains a duplicate field")
		}
		seen[key] = struct{}{}
		switch key {
		case "version", "action", "message", "summary", "reason":
		default:
			return rootMessagePreview{}, errors.New("root lifecycle preview contains an unknown field")
		}

		position = skipJSONSpace(raw, next)
		if position == len(raw) {
			return current(), nil
		}
		if raw[position] != ':' {
			return rootMessagePreview{}, errors.New("root lifecycle field is missing a colon")
		}
		position = skipJSONSpace(raw, position+1)
		if position == len(raw) {
			return current(), nil
		}
		value, next, complete, err := parseJSONStringPrefix(raw, position)
		if err != nil {
			return rootMessagePreview{}, err
		}
		switch key {
		case "version":
			if complete {
				version = value
			}
		case "action":
			if complete {
				action = value
			}
		case "message":
			messageSeen = true
			message = value
			messageComplete = complete
		}
		if !complete {
			return current(), nil
		}
		position = next
	}
}

func validRootPreviewAction(value string) bool {
	switch domain.RootActionKind(value) {
	case domain.RootActionContinue, domain.RootActionFinish, domain.RootActionWait:
		return true
	default:
		return false
	}
}

func skipJSONSpace(value string, position int) int {
	for position < len(value) {
		switch value[position] {
		case ' ', '\t', '\r', '\n':
			position++
		default:
			return position
		}
	}
	return position
}

func parseJSONStringPrefix(value string, position int) (string, int, bool, error) {
	if position >= len(value) || value[position] != '"' {
		return "", position, false, errors.New("root lifecycle fields must be JSON strings")
	}
	var decoded strings.Builder
	for position++; position < len(value); {
		current := value[position]
		if current == '"' {
			return decoded.String(), position + 1, true, nil
		}
		if current < 0x20 {
			return "", position, false, errors.New("root lifecycle string contains a control character")
		}
		if current != '\\' {
			r, size := utf8.DecodeRuneInString(value[position:])
			if r == utf8.RuneError && size == 1 {
				if !utf8.FullRuneInString(value[position:]) {
					return decoded.String(), len(value), false, nil
				}
				return "", position, false, errors.New("root lifecycle string is not valid UTF-8")
			}
			decoded.WriteRune(r)
			position += size
			continue
		}

		position++
		if position >= len(value) {
			return decoded.String(), len(value), false, nil
		}
		switch value[position] {
		case '"', '\\', '/':
			decoded.WriteByte(value[position])
			position++
		case 'b':
			decoded.WriteByte('\b')
			position++
		case 'f':
			decoded.WriteByte('\f')
			position++
		case 'n':
			decoded.WriteByte('\n')
			position++
		case 'r':
			decoded.WriteByte('\r')
			position++
		case 't':
			decoded.WriteByte('\t')
			position++
		case 'u':
			r, next, complete, err := decodeJSONUnicodeEscape(value, position+1)
			if err != nil || !complete {
				return decoded.String(), next, false, err
			}
			position = next
			if utf16.IsSurrogate(r) {
				if r < 0xD800 || r > 0xDBFF {
					return "", position, false, errors.New("root lifecycle string contains an invalid surrogate")
				}
				if position+2 > len(value) {
					return decoded.String(), len(value), false, nil
				}
				if value[position] != '\\' || value[position+1] != 'u' {
					return "", position, false, errors.New("root lifecycle string contains an unpaired surrogate")
				}
				low, lowNext, lowComplete, lowErr := decodeJSONUnicodeEscape(value, position+2)
				if lowErr != nil || !lowComplete {
					return decoded.String(), lowNext, false, lowErr
				}
				combined := utf16.DecodeRune(r, low)
				if combined == utf8.RuneError {
					return "", position, false, errors.New("root lifecycle string contains an invalid surrogate pair")
				}
				decoded.WriteRune(combined)
				position = lowNext
			} else {
				decoded.WriteRune(r)
			}
		default:
			return "", position, false, errors.New("root lifecycle string contains an invalid escape")
		}
	}
	return decoded.String(), len(value), false, nil
}

func decodeJSONUnicodeEscape(value string, position int) (rune, int, bool, error) {
	if len(value)-position < 4 {
		return 0, len(value), false, nil
	}
	var decoded rune
	for index := 0; index < 4; index++ {
		current := value[position+index]
		decoded <<= 4
		switch {
		case current >= '0' && current <= '9':
			decoded += rune(current - '0')
		case current >= 'a' && current <= 'f':
			decoded += rune(current-'a') + 10
		case current >= 'A' && current <= 'F':
			decoded += rune(current-'A') + 10
		default:
			return 0, position + index, false, errors.New("root lifecycle string contains an invalid Unicode escape")
		}
	}
	return decoded, position + 4, true, nil
}
