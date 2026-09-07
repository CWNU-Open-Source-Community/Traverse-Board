package modelregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	MaxProviderAdvancedConfigBytes = 64 << 10
	maxProviderAdvancedDepth       = 16
	maxProviderAdvancedNodes       = 2048
	maxProviderAdvancedKeyBytes    = 128
	maxProviderAdvancedStringBytes = 16 << 10
)

var reservedAdvancedHarnessKeys = map[string]struct{}{
	"background": {}, "conversation": {}, "include": {}, "input": {},
	"instructions": {}, "max_completion_tokens": {}, "max_output_tokens": {},
	"max_tokens": {}, "messages": {}, "model": {}, "parallel_tool_calls": {},
	"previous_response_id": {}, "response_format": {}, "store": {}, "stream": {},
	"stream_options": {}, "temperature": {}, "text": {}, "tool_choice": {},
	"tools": {}, "truncation": {},
}

var reservedAdvancedHeaders = map[string]struct{}{
	"accept": {}, "connection": {}, "content-length": {}, "content-type": {},
	"host": {}, "keep-alive": {},
	"proxy-authenticate": {}, "proxy-authorization": {}, "te": {}, "trailer": {},
	"transfer-encoding": {}, "upgrade": {},
}

// ValidateAndNormalizeProviderAdvancedConfig validates freely editable
// provider-specific JSON and returns a canonical object. It stores references
// to the owning Provider credential, never the credential itself. This
// function does not apply the config to a runtime request.
func ValidateAndNormalizeProviderAdvancedConfig(raw json.RawMessage,
	providerID string,
) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > MaxProviderAdvancedConfigBytes || !utf8.Valid(raw) {
		return nil, errors.New("custom Provider advanced config is too large or invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	value, err := decodeAdvancedConfigNode(decoder, 1, &nodes)
	if err != nil {
		return nil, fmt.Errorf("custom Provider advanced config is malformed: %w", err)
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return nil, errors.New("custom Provider advanced config contains trailing data")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("custom Provider advanced config must be a JSON object")
	}
	if _, credentialOnly := root["$credential"]; credentialOnly {
		return nil, errors.New("custom Provider advanced config root cannot be a credential reference")
	}
	if err := validateAdvancedConfigNode(root, "", providerID, 1); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(root)
	if err != nil || len(encoded) > MaxProviderAdvancedConfigBytes {
		return nil, errors.New("custom Provider advanced config could not be normalized")
	}
	return json.RawMessage(encoded), nil
}

func decodeAdvancedConfigNode(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if depth > maxProviderAdvancedDepth {
		return nil, errors.New("maximum nesting depth was exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	*nodes++
	if *nodes > maxProviderAdvancedNodes {
		return nil, errors.New("maximum node count was exceeded")
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		switch value := token.(type) {
		case nil, bool, json.Number:
			return value, nil
		case string:
			if !utf8.ValidString(value) || len([]byte(value)) > maxProviderAdvancedStringBytes {
				return nil, errors.New("string value is invalid or too large")
			}
			return value, nil
		default:
			return nil, errors.New("unsupported JSON value")
		}
	}
	switch delimiter {
	case '{':
		value := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok || key == "" || !utf8.ValidString(key) ||
				len([]byte(key)) > maxProviderAdvancedKeyBytes {
				return nil, errors.New("object key is invalid")
			}
			*nodes++
			if *nodes > maxProviderAdvancedNodes {
				return nil, errors.New("maximum node count was exceeded")
			}
			if _, duplicate := value[key]; duplicate {
				return nil, errors.New("duplicate object key is not allowed")
			}
			child, err := decodeAdvancedConfigNode(decoder, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			value[key] = child
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return nil, errors.New("object is not closed")
		}
		return value, nil
	case '[':
		value := make([]any, 0)
		for decoder.More() {
			child, err := decodeAdvancedConfigNode(decoder, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return nil, errors.New("array is not closed")
		}
		return value, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

func validateAdvancedConfigNode(value any, key string, providerID string, depth int) error {
	if depth > maxProviderAdvancedDepth {
		return errors.New("custom Provider advanced config nesting is too deep")
	}
	switch current := value.(type) {
	case map[string]any:
		if reference, err := validateAdvancedCredentialReference(current, providerID); reference || err != nil {
			return err
		}
		headerContainer := advancedHeaderContainer(key)
		if headerContainer {
			seenHeaders := make(map[string]struct{}, len(current))
			for header, headerValue := range current {
				if err := validateAdvancedHeaderName(header); err != nil {
					return err
				}
				canonical := strings.ToLower(http.CanonicalHeaderKey(header))
				if _, duplicate := seenHeaders[canonical]; duplicate {
					return errors.New("custom Provider request header names must be unique ignoring case")
				}
				seenHeaders[canonical] = struct{}{}
				if _, isString := headerValue.(string); !isString {
					if reference, err := validateAdvancedCredentialReferenceValue(headerValue,
						providerID); err != nil || !reference {
						if err != nil {
							return err
						}
						return errors.New("custom Provider request header values must be strings or credential references")
					}
				}
			}
		}
		enforceHarnessKeys := depth == 1 || advancedRequestBodyContainer(key)
		for childKey, child := range current {
			if err := validateAdvancedConfigKey(childKey); err != nil {
				return err
			}
			if enforceHarnessKeys {
				if _, reserved := reservedAdvancedHarnessKeys[strings.ToLower(childKey)]; reserved {
					return fmt.Errorf("custom Provider advanced config cannot override Harness key %q", childKey)
				}
			}
			if depth == 1 {
				if normalized, semantic := canonicalAdvancedSemanticContainer(childKey); semantic &&
					childKey != normalized {
					return fmt.Errorf("custom Provider advanced config container %q must use canonical name %q",
						childKey, normalized)
				}
				if err := validateAdvancedSemanticContainer(childKey, child, providerID); err != nil {
					return err
				}
			}
			if sensitiveAdvancedConfigKey(childKey) {
				reference, err := validateAdvancedCredentialReferenceValue(child, providerID)
				if err != nil {
					return err
				}
				if !reference {
					return fmt.Errorf("custom Provider advanced config key %q requires an owning credential reference", childKey)
				}
				continue
			}
			if err := validateAdvancedConfigNode(child, childKey, providerID, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := validateAdvancedConfigNode(child, key, providerID, depth+1); err != nil {
				return err
			}
		}
	case string:
		return validateAdvancedConfigString(current, key)
	case nil, bool, json.Number:
		return nil
	default:
		return errors.New("custom Provider advanced config contains an unsupported value")
	}
	return nil
}

func validateAdvancedCredentialReferenceValue(value any, providerID string) (bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return false, nil
	}
	return validateAdvancedCredentialReference(object, providerID)
}

func validateAdvancedCredentialReference(value map[string]any,
	providerID string,
) (bool, error) {
	referenceValue, found := value["$credential"]
	if !found {
		return false, nil
	}
	if len(value) < 1 || len(value) > 2 {
		return true, errors.New("custom Provider credential reference contains unsupported fields")
	}
	for key := range value {
		if key != "$credential" && key != "template" {
			return true, errors.New("custom Provider credential reference contains unsupported fields")
		}
	}
	reference, ok := referenceValue.(string)
	if !ok || reference != providerID {
		return true, errors.New("custom Provider credential reference must point to its owning Provider")
	}
	if templateValue, configured := value["template"]; configured {
		template, ok := templateValue.(string)
		if !ok || len([]byte(template)) > 256 || strings.Count(template, "${secret}") != 1 ||
			strings.ContainsAny(template, "\r\n") {
			return true, errors.New("custom Provider credential reference template is invalid")
		}
		for _, current := range template {
			if unicode.IsControl(current) {
				return true, errors.New("custom Provider credential reference template is invalid")
			}
		}
		probe := strings.Replace(template, "${secret}", "x", 1)
		if redact.String(probe) != probe {
			return true, errors.New("custom Provider credential reference template contains inline credential material")
		}
		if strings.Contains(probe, "://") {
			parsed, err := url.Parse(probe)
			if err != nil || !parsed.IsAbs() || parsed.User != nil {
				return true, errors.New("custom Provider credential reference template cannot create URL userinfo")
			}
			for queryKey := range parsed.Query() {
				if sensitiveAdvancedConfigKey(queryKey) {
					return true, errors.New("custom Provider credential reference template cannot create a credential URL")
				}
			}
		}
	}
	return true, nil
}

func validateAdvancedConfigKey(value string) error {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		len([]byte(value)) > maxProviderAdvancedKeyBytes || redact.String(value) != value {
		return errors.New("custom Provider advanced config key is invalid")
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return errors.New("custom Provider advanced config key is invalid")
		}
	}
	return nil
}

func validateAdvancedConfigString(value string, key string) error {
	if !utf8.ValidString(value) || len([]byte(value)) > maxProviderAdvancedStringBytes ||
		redact.String(value) != value || strings.Contains(value, "${secret}") {
		return fmt.Errorf("custom Provider advanced config value for %q contains credential material", key)
	}
	for _, current := range value {
		if unicode.IsControl(current) && current != '\t' {
			return errors.New("custom Provider advanced config string contains control characters")
		}
	}
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") {
		return errors.New("custom Provider advanced config cannot contain an inline authorization value")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil && parsed.IsAbs() {
			if parsed.User != nil {
				return errors.New("custom Provider advanced config URL cannot contain userinfo")
			}
			for queryKey := range parsed.Query() {
				if sensitiveAdvancedConfigKey(queryKey) {
					return errors.New("custom Provider advanced config URL cannot contain credential query fields")
				}
			}
		}
	}
	return nil
}

func advancedHeaderContainer(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "request_headers":
		return true
	default:
		return false
	}
}

func advancedRequestBodyContainer(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), "request_body")
}

func validateAdvancedSemanticContainer(key string, value any, providerID string) error {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "env", "model_mapping", "request_body", "request_headers":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("custom Provider advanced config %s must be an object", normalized)
		}
		if normalized == "env" {
			for name, envValue := range object {
				if err := validateAdvancedConfigKey(name); err != nil {
					return errors.New("custom Provider advanced config environment name is invalid")
				}
				if _, isString := envValue.(string); isString {
					continue
				}
				reference, err := validateAdvancedCredentialReferenceValue(envValue, providerID)
				if err != nil {
					return err
				}
				if !reference {
					return errors.New("custom Provider advanced config environment values must be strings or credential references")
				}
			}
		}
		if normalized == "model_mapping" {
			for local, mappedValue := range object {
				mapped, ok := mappedValue.(string)
				if !ok || !validAvailabilityIdentifier(local, maxPublicModelNameBytes) ||
					!validAvailabilityIdentifier(mapped, maxPublicModelNameBytes) {
					return errors.New("custom Provider model mapping values must be model identifiers")
				}
			}
		}
	}
	return nil
}

func canonicalAdvancedSemanticContainer(key string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "env":
		return "env", true
	case "model_mapping":
		return "model_mapping", true
	case "request_body":
		return "request_body", true
	case "request_headers":
		return "request_headers", true
	default:
		return "", false
	}
}

func validateAdvancedHeaderName(value string) error {
	if err := validateAdvancedConfigKey(value); err != nil ||
		http.CanonicalHeaderKey(value) == "" || strings.ContainsRune(value, ':') {
		return errors.New("custom Provider request header name is invalid")
	}
	if _, reserved := reservedAdvancedHeaders[strings.ToLower(value)]; reserved {
		return fmt.Errorf("custom Provider request header %q is reserved", value)
	}
	return nil
}

func sensitiveAdvancedConfigKey(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(normalized)
	switch normalized {
	case "$credential", "access_key", "access_key_id", "access_token", "api_key",
		"apikey", "auth", "authorization", "client_secret", "cookie", "credential",
		"credentials", "jwt", "oauth_token", "password", "passwd", "private_key",
		"proxy_authorization", "secret", "session_secret", "session_token", "set_cookie",
		"sig", "signature", "token", "x_amz_credential", "x_amz_signature", "x_api_key",
		"x_goog_credential", "x_goog_signature":
		return true
	}
	for _, suffix := range []string{"_access_key", "_access_token", "_api_key", "_apikey",
		"_authorization", "_client_secret", "_cookie", "_credential", "_credentials",
		"_oauth_token", "_password", "_passwd", "_private_key", "_secret",
		"_session_token", "_signature", "_token"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
