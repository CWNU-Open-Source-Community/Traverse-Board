package codeintel

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

var markdownLinkPattern = regexp.MustCompile(`\[([^\]\r\n]{0,512})\]\(([^)\s]{1,1000})\)`)

var markdownReferenceDefinitionPattern = regexp.MustCompile(
	`(?mi)^[ \t]{0,3}\[[^\]\r\n]{1,512}\]:[ \t]*\S[^\r\n]*$`)

var markdownEmbeddedLinkTagPattern = regexp.MustCompile(
	`(?i)<[ \t]*/?[ \t]*(?:a|img|iframe|script|link|source|video|audio)\b[^>\r\n]*>`)

var markdownURITextPattern = regexp.MustCompile(
	`(?i)(?:https?|ftp|mailto|data|javascript|file):[^\s<>"')\]]+`)

func sanitizeText(value string, maxBytes int, multiline bool) (string, bool) {
	value = strings.ToValidUTF8(value, "?")
	value = redact.String(value)
	var builder strings.Builder
	builder.Grow(min(len(value), maxBytes))
	truncated := false
	for _, current := range value {
		if unicode.IsControl(current) && current != '\t' &&
			!(multiline && (current == '\n' || current == '\r')) {
			truncated = true
			continue
		}
		encodedBytes := utf8.RuneLen(current)
		if encodedBytes < 0 || builder.Len()+encodedBytes > maxBytes {
			truncated = true
			break
		}
		builder.WriteRune(current)
	}
	return strings.TrimSpace(builder.String()), truncated
}

func sanitizeMarkdown(root, value string) (string, bool) {
	value, truncated := sanitizeText(value, MaxMarkdownBytes, true)
	links := 0
	value = markdownLinkPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		links++
		parts := markdownLinkPattern.FindStringSubmatch(candidate)
		if len(parts) != 3 {
			truncated = true
			return "[link omitted]"
		}
		label, _ := sanitizeText(parts[1], 512, false)
		if label == "" {
			label = "link"
		}
		if links > MaxLinks {
			truncated = true
			return label
		}
		if relative, _, err := workspaceRelativeURI(root, parts[2]); err == nil {
			return label + " (`" + relative + "`)"
		}
		// Remote or malformed targets are not propagated. The human-readable
		// label remains useful evidence without creating an ambient navigation
		// or credential exfiltration channel.
		truncated = true
		return label
	})
	value = markdownReferenceDefinitionPattern.ReplaceAllStringFunc(value, func(string) string {
		truncated = true
		return "[link definition omitted]"
	})
	value = markdownEmbeddedLinkTagPattern.ReplaceAllStringFunc(value, func(string) string {
		truncated = true
		return "[embedded link omitted]"
	})
	value = markdownURITextPattern.ReplaceAllStringFunc(value, func(string) string {
		truncated = true
		return "[remote target omitted]"
	})
	if len(value) > MaxMarkdownBytes {
		value = truncateUTF8(value, MaxMarkdownBytes)
		truncated = true
	}
	return value, truncated
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return value
}

type boundedBuffer struct {
	mu        sync.Mutex
	limit     int
	value     []byte
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit, value: make([]byte, 0, min(limit, 4096))}
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(value)
	remaining := b.limit - len(b.value)
	if remaining <= 0 {
		b.truncated = b.truncated || len(value) > 0
		return written, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	b.value = append(b.value, value...)
	return written, nil
}

func (b *boundedBuffer) SafeString() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	value := string(bytes.Clone(b.value))
	safe, cleaned := sanitizeText(value, b.limit, true)
	return safe, b.truncated || cleaned
}
