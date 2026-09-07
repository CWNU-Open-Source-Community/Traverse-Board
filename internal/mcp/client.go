package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"cyberagent-workbench/internal/redact"
)

type Client struct {
	transport  clientTransport
	descriptor ServerDescriptor
	secrets    []string
	nextID     atomic.Uint64
	closed     atomic.Bool
}

func newClient(transport clientTransport, descriptor ServerDescriptor, secrets ...string) *Client {
	filtered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			filtered = append(filtered, secret)
		}
	}
	return &Client{transport: transport, descriptor: descriptor, secrets: filtered}
}

type clientInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    struct {
		Tools     json.RawMessage `json:"tools,omitempty"`
		Resources json.RawMessage `json:"resources,omitempty"`
		Prompts   json.RawMessage `json:"prompts,omitempty"`
	} `json:"capabilities"`
	ServerInfo ClientInfo `json:"serverInfo"`
}

func (c *Client) Discover(ctx context.Context, at time.Time) (CapabilitySnapshot, error) {
	if c == nil || c.transport == nil || c.closed.Load() {
		return CapabilitySnapshot{}, errors.New("MCP client is closed")
	}
	params := map[string]any{"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{},
		"clientInfo":   map[string]string{"name": ClientName, "version": ClientVersion}}
	var initialized clientInitializeResult
	if err := c.request(ctx, "initialize", params, &initialized); err != nil {
		return CapabilitySnapshot{}, err
	}
	initialized.ServerInfo.Name = c.sanitizeText(initialized.ServerInfo.Name)
	initialized.ServerInfo.Version = c.sanitizeText(initialized.ServerInfo.Version)
	if initialized.ProtocolVersion != ProtocolVersion ||
		!validClientIdentity(initialized.ServerInfo.Name) ||
		!validClientText(initialized.ServerInfo.Version, 128, false) {
		return CapabilitySnapshot{}, errors.New("MCP initialize response has an unsupported protocol or invalid server identity")
	}
	advertised := make([]CapabilityKind, 0, 3)
	if capabilityPresent(initialized.Capabilities.Tools) {
		advertised = append(advertised, CapabilityTools)
	}
	if capabilityPresent(initialized.Capabilities.Resources) {
		advertised = append(advertised, CapabilityResources)
	}
	if capabilityPresent(initialized.Capabilities.Prompts) {
		advertised = append(advertised, CapabilityPrompts)
	}
	for _, capability := range advertised {
		if !slices.Contains(c.descriptor.DeclaredCapabilities, capability) {
			return CapabilitySnapshot{}, fmt.Errorf("MCP server advertised undeclared %s capability", capability)
		}
	}
	if err := c.transport.Notify(ctx, Envelope{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return CapabilitySnapshot{}, err
	}
	var tools []RemoteTool
	var resources []RemoteResource
	var prompts []RemotePrompt
	var err error
	if slices.Contains(advertised, CapabilityTools) {
		tools, err = c.listTools(ctx)
	}
	if err == nil && slices.Contains(advertised, CapabilityResources) {
		resources, err = c.listResources(ctx)
	}
	if err == nil && slices.Contains(advertised, CapabilityPrompts) {
		prompts, err = c.listPrompts(ctx)
	}
	if err != nil {
		return CapabilitySnapshot{}, err
	}
	return NewCapabilitySnapshot(initialized.ServerInfo.Name, initialized.ServerInfo.Version,
		advertised, tools, resources, prompts, at)
}

func capabilityPresent(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	return len(value) > 0 && !bytes.Equal(value, []byte("null"))
}

type toolListResult struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (c *Client) listTools(ctx context.Context) ([]RemoteTool, error) {
	items := make([]RemoteTool, 0)
	cursor := ""
	for page := 0; page < 16; page++ {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result toolListResult
		if err := c.request(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		for _, item := range result.Tools {
			schema, err := c.sanitizeJSON(item.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("sanitize MCP tool schema: %w", err)
			}
			candidate := RemoteTool{Name: strings.TrimSpace(c.sanitizeText(item.Name)),
				Description: strings.TrimSpace(c.sanitizeText(item.Description)),
				InputSchema: schema}
			if err := candidate.Validate(); err != nil {
				return nil, fmt.Errorf("invalid MCP tool definition: %w", err)
			}
			items = append(items, candidate)
			if len(items) > MaxClientTools {
				return nil, errors.New("MCP tool catalog exceeds its limit")
			}
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			return items, nil
		}
		if !validClientText(cursor, 4096, false) {
			return nil, errors.New("MCP tool pagination cursor is invalid")
		}
	}
	return nil, errors.New("MCP tool pagination exceeded its page limit")
}

type resourceListResult struct {
	Resources []struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		MIMEType    string `json:"mimeType,omitempty"`
	} `json:"resources"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (c *Client) listResources(ctx context.Context) ([]RemoteResource, error) {
	items := make([]RemoteResource, 0)
	cursor := ""
	for page := 0; page < 16; page++ {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result resourceListResult
		if err := c.request(ctx, "resources/list", params, &result); err != nil {
			return nil, err
		}
		for _, item := range result.Resources {
			candidate := RemoteResource{URI: strings.TrimSpace(c.sanitizeText(item.URI)),
				Name:        strings.TrimSpace(c.sanitizeText(item.Name)),
				Description: strings.TrimSpace(c.sanitizeText(item.Description)),
				MIMEType:    strings.TrimSpace(c.sanitizeText(item.MIMEType))}
			if err := candidate.Validate(); err != nil {
				return nil, fmt.Errorf("invalid MCP resource definition: %w", err)
			}
			items = append(items, candidate)
			if len(items) > MaxClientResources {
				return nil, errors.New("MCP resource catalog exceeds its limit")
			}
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			return items, nil
		}
		if !validClientText(cursor, 4096, false) {
			return nil, errors.New("MCP resource pagination cursor is invalid")
		}
	}
	return nil, errors.New("MCP resource pagination exceeded its page limit")
}

type promptListResult struct {
	Prompts []struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	} `json:"prompts"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (c *Client) listPrompts(ctx context.Context) ([]RemotePrompt, error) {
	items := make([]RemotePrompt, 0)
	cursor := ""
	for page := 0; page < 16; page++ {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result promptListResult
		if err := c.request(ctx, "prompts/list", params, &result); err != nil {
			return nil, err
		}
		for _, item := range result.Prompts {
			candidate := RemotePrompt{Name: strings.TrimSpace(c.sanitizeText(item.Name)),
				Description: strings.TrimSpace(c.sanitizeText(item.Description))}
			if err := candidate.Validate(); err != nil {
				return nil, fmt.Errorf("invalid MCP prompt definition: %w", err)
			}
			items = append(items, candidate)
			if len(items) > MaxClientPrompts {
				return nil, errors.New("MCP prompt catalog exceeds its limit")
			}
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			return items, nil
		}
		if !validClientText(cursor, 4096, false) {
			return nil, errors.New("MCP prompt pagination cursor is invalid")
		}
	}
	return nil, errors.New("MCP prompt pagination exceeded its page limit")
}

type ClientCallResult struct {
	Content   string
	IsError   bool
	Bytes     int
	Truncated bool
}

func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage,
	maxBytes int,
) (ClientCallResult, error) {
	if !validRemoteName(name) || len(arguments) == 0 || len(arguments) > MaxClientArgumentsBytes ||
		!json.Valid(arguments) || len(bytes.TrimSpace(arguments)) == 0 || bytes.TrimSpace(arguments)[0] != '{' {
		return ClientCallResult{}, errors.New("MCP tool call name or arguments are invalid")
	}
	if maxBytes < 1 || maxBytes > MaxClientResultBytes {
		return ClientCallResult{}, errors.New("MCP tool result bound is invalid")
	}
	var result struct {
		Content           json.RawMessage `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
		IsError           bool            `json:"isError,omitempty"`
	}
	if err := c.request(ctx, "tools/call", struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{Name: name, Arguments: arguments}, &result); err != nil {
		return ClientCallResult{}, err
	}
	value := result.Content
	if len(result.StructuredContent) > 0 && !bytes.Equal(bytes.TrimSpace(result.StructuredContent), []byte("null")) {
		value, _ = json.Marshal(map[string]json.RawMessage{"content": result.Content,
			"structured_content": result.StructuredContent})
	}
	if len(value) == 0 || !json.Valid(value) {
		return ClientCallResult{}, errors.New("MCP tool result content is invalid")
	}
	value, err := c.sanitizeJSON(value)
	if err != nil {
		return ClientCallResult{}, errors.New("MCP tool result content could not be sanitized")
	}
	value, err = redact.SanitizeSensitiveJSON(value)
	if err != nil {
		return ClientCallResult{}, errors.New("MCP tool result sensitive fields could not be sanitized")
	}
	content := string(value)
	truncated := len([]byte(content)) > maxBytes
	if truncated {
		content = truncateClientUTF8(content, maxBytes)
	}
	return ClientCallResult{Content: content, IsError: result.IsError,
		Bytes: len([]byte(content)), Truncated: truncated}, nil
}

func (c *Client) request(ctx context.Context, method string, params any, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id := c.nextID.Add(1)
	idRaw := json.RawMessage(fmt.Sprintf("%d", id))
	response, err := c.transport.Exchange(ctx, Envelope{JSONRPC: "2.0", ID: idRaw,
		Method: method, Params: raw})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errors.New(c.sanitizeText(err.Error()))
	}
	if !bytes.Equal(bytes.TrimSpace(response.ID), idRaw) || response.Method != "" {
		return errors.New("MCP response identity does not match its request")
	}
	if response.Error != nil {
		return fmt.Errorf("MCP %s failed with remote code %d", method, response.Error.Code)
	}
	if len(response.Result) == 0 || len(response.Result) > MaxMessageBytes {
		return errors.New("MCP response result is missing or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Result))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode MCP %s result: %w", method, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("MCP response result contains trailing JSON")
	}
	return nil
}

func (c *Client) sanitizeText(value string) string {
	value = strings.ToValidUTF8(value, "?")
	for _, secret := range c.secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED:credential]")
	}
	return redact.String(value)
}

func (c *Client) sanitizeJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxMessageBytes || !json.Valid(raw) {
		return nil, errors.New("remote JSON is missing, invalid, or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	value, err := c.sanitizeJSONValue(value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxMessageBytes {
		return nil, errors.New("sanitized remote JSON exceeds its bound")
	}
	return encoded, nil
}

func (c *Client) sanitizeJSONValue(value any) (any, error) {
	switch current := value.(type) {
	case string:
		return c.sanitizeText(current), nil
	case []any:
		for index := range current {
			var err error
			current[index], err = c.sanitizeJSONValue(current[index])
			if err != nil {
				return nil, err
			}
		}
		return current, nil
	case map[string]any:
		sanitized := make(map[string]any, len(current))
		for key, item := range current {
			safeKey := c.sanitizeText(key)
			if _, exists := sanitized[safeKey]; exists {
				return nil, errors.New("credential redaction caused a duplicate remote JSON field")
			}
			safeItem, err := c.sanitizeJSONValue(item)
			if err != nil {
				return nil, err
			}
			sanitized[safeKey] = safeItem
		}
		return sanitized, nil
	default:
		return current, nil
	}
}

func (c *Client) Close() error {
	if c == nil || c.closed.Swap(true) {
		return nil
	}
	return c.transport.Close()
}
