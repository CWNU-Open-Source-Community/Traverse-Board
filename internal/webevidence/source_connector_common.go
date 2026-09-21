package webevidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

func connectorGet(ctx context.Context, client *SafeHTTPClient, rawURL, accept string,
	authority NetworkAuthority,
) (HTTPDocument, error) {
	if client == nil {
		return HTTPDocument{}, newConnectorError("connector_unavailable", nil)
	}
	document, err := client.GetAuthorized(ctx, rawURL, DefaultMaxResponse, accept,
		func(current string) error {
			_, authorizeErr := authority.Authorize(current)
			return authorizeErr
		})
	if err != nil {
		code := "network_unavailable"
		if errors.Is(err, context.Canceled) {
			code = "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		return HTTPDocument{}, &connectorError{code: code, err: err, endpoint: rawURL}
	}
	switch document.StatusCode {
	case http.StatusUnauthorized:
		return HTTPDocument{}, newConnectorHTTPError("authentication_required", document, nil)
	case http.StatusForbidden:
		code := "access_blocked"
		body := strings.ToLower(boundedConnectorText(string(document.Body), 4096))
		if strings.TrimSpace(document.Header.Get("X-RateLimit-Remaining")) == "0" ||
			strings.TrimSpace(document.Header.Get("Retry-After")) != "" ||
			strings.Contains(body, "rate limit") || strings.Contains(body, "abuse detection") {
			code = "rate_limited"
		}
		return HTTPDocument{}, newConnectorHTTPError(code, document, nil)
	case http.StatusTooManyRequests:
		return HTTPDocument{}, newConnectorHTTPError("rate_limited", document, nil)
	case http.StatusNotFound:
		return HTTPDocument{}, newConnectorHTTPError("not_found", document, nil)
	}
	if document.StatusCode < http.StatusOK || document.StatusCode >= http.StatusMultipleChoices {
		return HTTPDocument{}, newConnectorHTTPError("remote_error", document,
			fmt.Errorf("HTTP %d", document.StatusCode))
	}
	if document.Truncated {
		return HTTPDocument{}, newConnectorHTTPError("response_too_large", document, nil)
	}
	return document, nil
}

func connectorGetJSON(ctx context.Context, client *SafeHTTPClient, rawURL string,
	authority NetworkAuthority, target any,
) (HTTPDocument, error) {
	document, err := connectorGet(ctx, client, rawURL, "application/json", authority)
	if err != nil {
		return HTTPDocument{}, err
	}
	mediaType, _, parseErr := mime.ParseMediaType(document.Header.Get("Content-Type"))
	if parseErr != nil || (mediaType != "application/json" &&
		mediaType != "application/vnd.github+json") {
		return HTTPDocument{}, newConnectorHTTPError("invalid_response", document,
			errors.New("connector response MIME is not JSON"))
	}
	decoder := json.NewDecoder(bytes.NewReader(document.Body))
	if err := decoder.Decode(target); err != nil {
		return HTTPDocument{}, newConnectorHTTPError("invalid_response", document, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return HTTPDocument{}, newConnectorHTTPError("invalid_response", document,
			errors.New("connector response contains trailing JSON"))
	}
	return document, nil
}

func combineConnectorRawDigest(documents ...HTTPDocument) string {
	hashInput := make([]byte, 0)
	for _, document := range documents {
		hashInput = append(hashInput, document.Body...)
		hashInput = append(hashInput, 0)
	}
	return DigestBytes(hashInput)
}

func connectorHTMLText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	nodes, err := xhtml.ParseFragment(strings.NewReader(value), nil)
	if err != nil {
		return boundedConnectorText(html.UnescapeString(value), MaxBodyBytes)
	}
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			builder.WriteString(node.Data)
			builder.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		switch node.Data {
		case "p", "br", "li", "blockquote", "pre":
			builder.WriteByte('\n')
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return boundedConnectorText(html.UnescapeString(builder.String()), MaxBodyBytes)
}

func boundedConnectorText(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "�")
	var builder strings.Builder
	lastSpace := false
	lastNewline := false
	for _, current := range value {
		switch {
		case current == '\r':
			continue
		case current == '\n':
			if !lastNewline {
				builder.WriteByte('\n')
			}
			lastNewline, lastSpace = true, false
		case unicode.IsSpace(current):
			if !lastSpace && !lastNewline {
				builder.WriteByte(' ')
			}
			lastSpace = true
		default:
			if unicode.IsControl(current) {
				continue
			}
			builder.WriteRune(current)
			lastSpace, lastNewline = false, false
		}
	}
	result := strings.TrimSpace(builder.String())
	if maxBytes <= 0 || len([]byte(result)) <= maxBytes {
		return result
	}
	raw := []byte(result)[:maxBytes]
	for len(raw) > 0 && !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	return strings.TrimSpace(string(raw))
}

func markdownSection(builder *strings.Builder, heading, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	if heading != "" {
		builder.WriteString(heading)
		builder.WriteString("\n\n")
	}
	builder.WriteString(body)
}
