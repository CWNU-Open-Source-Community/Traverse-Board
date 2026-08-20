package codeintel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type lspLocation struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type lspLocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

func parseLocations(root string, raw json.RawMessage, kind string) ([]EvidenceItem, []string) {
	if string(raw) == "null" || len(raw) == 0 {
		return []EvidenceItem{}, nil
	}
	values := splitObjectOrArray(raw)
	items := make([]EvidenceItem, 0, min(len(values), MaxResultItems))
	warnings := make([]string, 0)
	for _, value := range values {
		var probe map[string]json.RawMessage
		if json.Unmarshal(value, &probe) != nil {
			warnings = append(warnings, "language server returned a malformed location")
			continue
		}
		uri := ""
		var valueRange Range
		var selection *Range
		if _, linked := probe["targetUri"]; linked {
			var link lspLocationLink
			if json.Unmarshal(value, &link) != nil || link.TargetRange.Validate() != nil ||
				link.TargetSelectionRange.Validate() != nil {
				warnings = append(warnings, "language server returned an invalid location link")
				continue
			}
			uri = link.TargetURI
			valueRange = link.TargetRange
			selectionCopy := link.TargetSelectionRange
			selection = &selectionCopy
		} else {
			var location lspLocation
			if json.Unmarshal(value, &location) != nil || location.Range.Validate() != nil {
				warnings = append(warnings, "language server returned an invalid location")
				continue
			}
			uri = location.URI
			valueRange = location.Range
		}
		path, _, err := workspaceRelativeURI(root, uri)
		if err != nil {
			warnings = append(warnings, "language server location outside the Workspace was discarded")
			continue
		}
		rangeCopy := valueRange
		item := EvidenceItem{Kind: kind, Path: path, Range: &rangeCopy, Selection: selection}
		item.ID = evidenceItemID(item)
		items = append(items, item)
		if len(items) == MaxResultItems {
			warnings = append(warnings, "language server locations exceeded the item limit")
			break
		}
	}
	return deduplicateItems(items), warnings
}

func splitObjectOrArray(raw json.RawMessage) []json.RawMessage {
	raw = bytes.TrimSpace(raw)
	var values []json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return []json.RawMessage{}
	}
	if raw[0] == '[' && json.Unmarshal(raw, &values) == nil {
		return values
	}
	if raw[0] == '{' {
		return []json.RawMessage{append(json.RawMessage(nil), raw...)}
	}
	return []json.RawMessage{}
}

func parseSymbols(root string, raw json.RawMessage, documentPath string) ([]EvidenceItem, []string) {
	values := splitObjectOrArray(raw)
	items := make([]EvidenceItem, 0)
	warnings := make([]string, 0)
	for _, value := range values {
		parseSymbolNode(root, documentPath, value, "", 0, &items, &warnings)
		if len(items) >= MaxResultItems {
			warnings = append(warnings, "language server symbols exceeded the item limit")
			break
		}
	}
	if len(items) > MaxResultItems {
		items = items[:MaxResultItems]
	}
	return deduplicateItems(items), warnings
}

func parseSymbolNode(root, documentPath string, raw json.RawMessage, parent string, depth int,
	items *[]EvidenceItem, warnings *[]string,
) {
	if depth > 64 || len(*items) >= MaxResultItems {
		return
	}
	var value struct {
		Name           string `json:"name"`
		Detail         string `json:"detail,omitempty"`
		Kind           int    `json:"kind"`
		ContainerName  string `json:"containerName,omitempty"`
		Range          *Range `json:"range,omitempty"`
		SelectionRange *Range `json:"selectionRange,omitempty"`
		Location       *struct {
			URI   string `json:"uri"`
			Range Range  `json:"range"`
		} `json:"location,omitempty"`
		Children []json.RawMessage `json:"children,omitempty"`
	}
	if json.Unmarshal(raw, &value) != nil {
		*warnings = append(*warnings, "language server returned a malformed symbol")
		return
	}
	name, cleanedName := sanitizeText(value.Name, 1024, false)
	detail, cleanedDetail := sanitizeText(value.Detail, 4096, true)
	container, cleanedContainer := sanitizeText(value.ContainerName, 1024, false)
	if container == "" {
		container = parent
	}
	if name == "" || value.Kind < 1 || value.Kind > 26 {
		*warnings = append(*warnings, "language server returned an invalid symbol identity")
		return
	}
	path := documentPath
	var symbolRange *Range
	selection := value.SelectionRange
	if value.Location != nil {
		resolved, _, err := workspaceRelativeURI(root, value.Location.URI)
		if err != nil || value.Location.Range.Validate() != nil {
			*warnings = append(*warnings, "language server symbol outside the Workspace was discarded")
			return
		}
		path = resolved
		rangeCopy := value.Location.Range
		symbolRange = &rangeCopy
	} else if value.Range != nil && value.Range.Validate() == nil {
		rangeCopy := *value.Range
		symbolRange = &rangeCopy
	} else {
		*warnings = append(*warnings, "language server symbol lacks a valid range")
		return
	}
	if path == "" {
		*warnings = append(*warnings, "language server symbol lacks a Workspace file")
		return
	}
	if selection != nil && selection.Validate() != nil {
		selection = nil
		*warnings = append(*warnings, "language server symbol selection range was discarded")
	}
	item := EvidenceItem{Kind: "symbol", Name: name, Detail: detail, Path: path,
		Range: symbolRange, Selection: selection,
		Metadata: map[string]string{"symbol_kind": strconv.Itoa(value.Kind)}}
	if container != "" {
		item.Metadata["container"] = container
	}
	item.ID = evidenceItemID(item)
	*items = append(*items, item)
	if cleanedName || cleanedDetail || cleanedContainer {
		*warnings = append(*warnings, "language server symbol text was sanitized")
	}
	for _, child := range value.Children {
		parseSymbolNode(root, documentPath, child, name, depth+1, items, warnings)
	}
}

func parseHover(root string, raw json.RawMessage, documentPath string) (
	string, *EvidenceItem, []string,
) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var value struct {
		Contents json.RawMessage `json:"contents"`
		Range    *Range          `json:"range,omitempty"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return "", nil, []string{"language server returned malformed hover content"}
	}
	content := extractMarkup(value.Contents)
	content, cleaned := sanitizeMarkdown(root, content)
	warnings := []string{}
	if cleaned {
		warnings = append(warnings, "hover Markdown or links were sanitized")
	}
	var item *EvidenceItem
	if value.Range != nil {
		if value.Range.Validate() == nil {
			rangeCopy := *value.Range
			current := EvidenceItem{Kind: "hover", Path: documentPath, Range: &rangeCopy}
			current.ID = evidenceItemID(current)
			item = &current
		} else {
			warnings = append(warnings, "hover range was invalid and discarded")
		}
	}
	return content, item, warnings
}

func parseSignatureHelp(root string, raw json.RawMessage, documentPath string) (
	[]EvidenceItem, string, []string,
) {
	if len(raw) == 0 || string(raw) == "null" {
		return []EvidenceItem{}, "", nil
	}
	var value struct {
		Signatures []struct {
			Label         string          `json:"label"`
			Documentation json.RawMessage `json:"documentation,omitempty"`
			Parameters    []struct {
				Label         json.RawMessage `json:"label"`
				Documentation json.RawMessage `json:"documentation,omitempty"`
			} `json:"parameters,omitempty"`
		} `json:"signatures"`
		ActiveSignature int `json:"activeSignature,omitempty"`
		ActiveParameter int `json:"activeParameter,omitempty"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return nil, "", []string{"language server returned malformed signature help"}
	}
	items := make([]EvidenceItem, 0, min(len(value.Signatures), MaxResultItems))
	contentParts := make([]string, 0)
	warnings := make([]string, 0)
	for index, signature := range value.Signatures {
		if index >= MaxResultItems {
			warnings = append(warnings, "signature help exceeded the item limit")
			break
		}
		label, cleaned := sanitizeText(signature.Label, 4096, false)
		documentation, docCleaned := sanitizeMarkdown(root, extractMarkup(signature.Documentation))
		if label == "" {
			warnings = append(warnings, "empty signature label was discarded")
			continue
		}
		item := EvidenceItem{Kind: "signature", Name: label, Detail: documentation,
			Path: documentPath, Metadata: map[string]string{
				"signature_index": strconv.Itoa(index),
				"active":          strconv.FormatBool(index == value.ActiveSignature),
			}}
		item.ID = evidenceItemID(item)
		items = append(items, item)
		if index == value.ActiveSignature {
			contentParts = append(contentParts, label)
			if documentation != "" {
				contentParts = append(contentParts, documentation)
			}
		}
		if cleaned || docCleaned {
			warnings = append(warnings, "signature help text was sanitized")
		}
	}
	content, cleaned := sanitizeMarkdown(root, strings.Join(contentParts, "\n\n"))
	if cleaned {
		warnings = append(warnings, "signature help output was bounded")
	}
	return items, content, warnings
}

func parseDiagnostics(root string, raw json.RawMessage, documentPath, documentURI string) (
	[]EvidenceItem, []string,
) {
	if len(raw) == 0 || string(raw) == "null" {
		return []EvidenceItem{}, nil
	}
	var envelope struct {
		URI         string            `json:"uri,omitempty"`
		Items       []json.RawMessage `json:"items,omitempty"`
		Diagnostics []json.RawMessage `json:"diagnostics,omitempty"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, []string{"language server returned malformed diagnostics"}
	}
	if envelope.URI != "" {
		path, canonical, err := workspaceRelativeURI(root, envelope.URI)
		if err != nil || canonical != documentURI || path != documentPath {
			return nil, []string{"diagnostics for a different or unsafe document were discarded"}
		}
	}
	values := envelope.Items
	if len(values) == 0 {
		values = envelope.Diagnostics
	}
	items := make([]EvidenceItem, 0, min(len(values), MaxDiagnostics))
	warnings := make([]string, 0)
	for index, rawDiagnostic := range values {
		if index >= MaxDiagnostics {
			warnings = append(warnings, "diagnostics exceeded the item limit")
			break
		}
		var value struct {
			Range    Range           `json:"range"`
			Severity int             `json:"severity,omitempty"`
			Code     json.RawMessage `json:"code,omitempty"`
			Source   string          `json:"source,omitempty"`
			Message  string          `json:"message"`
			Tags     []int           `json:"tags,omitempty"`
		}
		if json.Unmarshal(rawDiagnostic, &value) != nil || value.Range.Validate() != nil ||
			value.Severity < 0 || value.Severity > 4 {
			warnings = append(warnings, "invalid diagnostic was discarded")
			continue
		}
		message, cleanedMessage := sanitizeText(value.Message, 8192, true)
		source, cleanedSource := sanitizeText(value.Source, 256, false)
		if message == "" {
			warnings = append(warnings, "empty diagnostic was discarded")
			continue
		}
		code := strings.Trim(string(value.Code), `"`)
		code, cleanedCode := sanitizeText(code, 256, false)
		tags := make([]string, 0, len(value.Tags))
		for _, tag := range value.Tags {
			switch tag {
			case 1:
				tags = append(tags, "unnecessary")
			case 2:
				tags = append(tags, "deprecated")
			}
		}
		rangeCopy := value.Range
		item := EvidenceItem{Kind: "diagnostic", Name: source, Detail: message,
			Path: documentPath, Range: &rangeCopy, Severity: value.Severity,
			Code: code, Tags: tags}
		item.ID = evidenceItemID(item)
		items = append(items, item)
		if cleanedMessage || cleanedSource || cleanedCode {
			warnings = append(warnings, "diagnostic text was sanitized")
		}
	}
	return deduplicateItems(items), warnings
}

func extractMarkup(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var object struct {
		Kind     string `json:"kind,omitempty"`
		Language string `json:"language,omitempty"`
		Value    string `json:"value,omitempty"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Value != "" {
		if object.Language != "" {
			return "```" + object.Language + "\n" + object.Value + "\n```"
		}
		return object.Value
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil {
		parts := make([]string, 0, len(values))
		for _, value := range values {
			if current := extractMarkup(value); current != "" {
				parts = append(parts, current)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func evidenceItemID(item EvidenceItem) string {
	rangeValue := ""
	if item.Range != nil {
		rangeValue = fmt.Sprintf("%d:%d-%d:%d", item.Range.Start.Line,
			item.Range.Start.Character, item.Range.End.Line, item.Range.End.Character)
	}
	metadataKeys := make([]string, 0, len(item.Metadata))
	for key := range item.Metadata {
		metadataKeys = append(metadataKeys, key)
	}
	sort.Strings(metadataKeys)
	metadata := make([]string, 0, len(metadataKeys)*2)
	for _, key := range metadataKeys {
		metadata = append(metadata, key, item.Metadata[key])
	}
	return digestStrings(item.Kind, item.Name, item.Detail, item.Path, rangeValue,
		item.Relationship, item.Code, strings.Join(item.Tags, "\x00"),
		strings.Join(metadata, "\x00"))
}

func deduplicateItems(values []EvidenceItem) []EvidenceItem {
	result := make([]EvidenceItem, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}
