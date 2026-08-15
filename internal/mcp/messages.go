package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// InitializeParams carries the client handshake. Only client identity and
// capabilities are accepted; every unknown field is rejected.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ClientInfo      ClientInfo      `json:"clientInfo"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func DecodeInitializeParams(raw json.RawMessage) (InitializeParams, error) {
	if len(raw) == 0 || len(raw) > MaxMessageBytes {
		return InitializeParams{}, fmt.Errorf("initialize params are missing or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var params InitializeParams
	if err := decoder.Decode(&params); err != nil {
		return InitializeParams{}, fmt.Errorf("initialize params are malformed: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return InitializeParams{}, fmt.Errorf("initialize params contain trailing data")
	}
	if params.ProtocolVersion != ProtocolVersion {
		return InitializeParams{}, fmt.Errorf("unsupported MCP protocol version %q; this server speaks %s only",
			params.ProtocolVersion, ProtocolVersion)
	}
	if len(params.ClientInfo.Name) == 0 || len(params.ClientInfo.Name) > 128 ||
		len(params.ClientInfo.Version) == 0 || len(params.ClientInfo.Version) > 128 {
		return InitializeParams{}, fmt.Errorf("client identity must be bounded and present")
	}
	return params, nil
}

// InitializeResult is the server capability declaration. Every declared
// capability is implemented; unimplemented tools are never published.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ClientInfo         `json:"serverInfo"`
}

type ServerCapabilities struct {
	Resources *struct{} `json:"resources,omitempty"`
	Tools     *struct{} `json:"tools,omitempty"`
}

// ListToolsResult is the bounded tool catalog.
type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// CallToolParams binds one typed action invocation. Only tool names and
// JSON arguments are accepted; arbitrary executables, paths, credentials,
// or permission tiers cannot be smuggled through extra fields.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func DecodeCallToolParams(raw json.RawMessage) (CallToolParams, error) {
	if len(raw) == 0 || len(raw) > MaxMessageBytes {
		return CallToolParams{}, fmt.Errorf("tool call params are missing or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var params CallToolParams
	if err := decoder.Decode(&params); err != nil {
		return CallToolParams{}, fmt.Errorf("tool call params are malformed: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return CallToolParams{}, fmt.Errorf("tool call params contain trailing data")
	}
	if params.Name == "" || len(params.Name) > 128 {
		return CallToolParams{}, fmt.Errorf("tool name must be bounded and present")
	}
	if len(params.Arguments) == 0 {
		return CallToolParams{}, fmt.Errorf("tool arguments are required")
	}
	if !json.Valid(params.Arguments) {
		return CallToolParams{}, fmt.Errorf("tool arguments must be a valid JSON object")
	}
	return params, nil
}

// CallToolResult is the redacted tool outcome.
type CallToolResult struct {
	Content []TextContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ListResourcesResult is the bounded resource catalog.
type ListResourcesResult struct {
	Resources []Resource `json:"resources"`
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ReadResourceParams binds one resource read by URI.
type ReadResourceParams struct {
	URI string `json:"uri"`
}

func DecodeReadResourceParams(raw json.RawMessage) (ReadResourceParams, error) {
	if len(raw) == 0 || len(raw) > MaxMessageBytes {
		return ReadResourceParams{}, fmt.Errorf("resource read params are missing or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var params ReadResourceParams
	if err := decoder.Decode(&params); err != nil {
		return ReadResourceParams{}, fmt.Errorf("resource read params are malformed: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ReadResourceParams{}, fmt.Errorf("resource read params contain trailing data")
	}
	if params.URI == "" || len(params.URI) > 512 {
		return ReadResourceParams{}, fmt.Errorf("resource URI must be bounded and present")
	}
	return params, nil
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
}

type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
}

// CancelledNotification is the accepted in-flight cancellation signal.
type CancelledNotification struct {
	RequestID json.RawMessage `json:"requestId"`
	Reason    string          `json:"reason,omitempty"`
}
