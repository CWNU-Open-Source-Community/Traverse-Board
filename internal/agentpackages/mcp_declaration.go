package agentpackages

import (
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// Validate portable shape only. B owns placeholder expansion, executable
// search, cwd/command filesystem resolution, platform environment semantics,
// origin-bound header delivery, transport availability and runtime admission.
// These strings carry no authority to start a process or make a request.
func validateServer(raw json.RawMessage) (string, error) {
	fields, err := jsonObject(raw)
	if err != nil {
		return "", err
	}
	transport, err := jsonString(fields["type"])
	if err != nil {
		return "", err
	}
	switch transport {
	case "stdio":
		for key := range fields {
			switch key {
			case "type", "command", "args", "env", "cwd":
			default:
				return "", errors.New("stdio_field_invalid")
			}
		}
		command, err := jsonString(fields["command"])
		if err != nil || !commandForm(command) {
			return "", errors.New("stdio_command_invalid")
		}
		if value, found := fields["args"]; found && !jsonStringArray(value) {
			return "", errors.New("stdio_args_invalid")
		}
		if value, found := fields["cwd"]; found {
			cwd, err := jsonString(value)
			if err != nil || !cwdForm(cwd) {
				return "", errors.New("stdio_cwd_invalid")
			}
		}
		if value, found := fields["env"]; found {
			environment, err := jsonObject(value)
			if err != nil {
				return "", errors.New("stdio_environment_invalid")
			}
			for key, value := range environment {
				if key == "PLUGIN_ROOT" || key == "PLUGIN_DATA" {
					return "", errors.New("stdio_reserved_environment")
				}
				if _, err := jsonString(value); err != nil {
					return "", errors.New("stdio_environment_invalid")
				}
			}
		}
	case "streamable-http", "sse":
		for key := range fields {
			switch key {
			case "type", "url", "headers":
			default:
				return "", errors.New("http_field_invalid")
			}
		}
		address, err := jsonString(fields["url"])
		if err != nil || !portableURL(address) {
			return "", errors.New("http_url_invalid")
		}
		if value, found := fields["headers"]; found {
			headers, err := jsonObject(value)
			if err != nil {
				return "", errors.New("http_headers_invalid")
			}
			seen := map[string]bool{}
			for key, value := range headers {
				text, err := jsonString(value)
				folded := strings.ToLower(key)
				if err != nil || !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(text) || seen[folded] {
					return "", errors.New("http_headers_invalid")
				}
				seen[folded] = true
			}
		}
	default:
		return "", errors.New("mcp_transport_unknown")
	}
	return transport, nil
}

func commandForm(command string) bool {
	if command == "" || strings.ContainsRune(command, 0) {
		return false
	}
	if strings.HasPrefix(command, "./") {
		return len(command) > 2
	}
	// A bare name is one OS executable token. Spaces inside a filename are
	// not split into a shell command; B passes this token and args separately.
	return command != "." && command != ".." && !strings.ContainsAny(command, "/\\:")
}

func cwdForm(cwd string) bool {
	return strings.HasPrefix(cwd, "./") || cwd == "${PLUGIN_ROOT}" || strings.HasPrefix(cwd, "${PLUGIN_ROOT}/") || cwd == "${PLUGIN_DATA}" || strings.HasPrefix(cwd, "${PLUGIN_DATA}/")
}

func portableURL(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || strings.Contains(address, "#") {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	if parsed.Hostname() == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(parsed.Hostname())
	return err == nil && ip.Unmap().IsLoopback()
}
