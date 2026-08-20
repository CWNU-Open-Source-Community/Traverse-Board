package codeintel

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

type hierarchyCandidate struct {
	raw  json.RawMessage
	item EvidenceItem
}

func (c *client) queryCallHierarchy(ctx context.Context, request Request,
	document *documentBinding,
) ([]EvidenceItem, []GraphEdge, string, []string, bool, error) {
	var prepared json.RawMessage
	if err := c.transport.request(ctx, "textDocument/prepareCallHierarchy",
		positionParams(document, request.Position), &prepared); err != nil {
		return nil, nil, "", nil, false, err
	}
	roots, warnings := parseHierarchyCandidates(c.workspace.Root, prepared, "call")
	if len(roots) > MaxHierarchyRoots {
		roots = roots[:MaxHierarchyRoots]
		warnings = append(warnings, "call hierarchy roots exceeded the expansion limit")
	}
	items := make([]EvidenceItem, 0)
	edges := make([]GraphEdge, 0)
	seen := make(map[string]struct{})
	add := func(item EvidenceItem) bool {
		if _, exists := seen[item.ID]; exists {
			return true
		}
		if len(items) >= MaxResultItems {
			return false
		}
		seen[item.ID] = struct{}{}
		items = append(items, item)
		return true
	}
	for _, root := range roots {
		root.item.Relationship = "root"
		root.item.ID = evidenceItemID(root.item)
		if !add(root.item) {
			warnings = append(warnings, "call hierarchy nodes exceeded the graph limit")
			break
		}
		if request.Direction == "incoming" || request.Direction == "both" {
			var raw json.RawMessage
			if err := c.transport.request(ctx, "callHierarchy/incomingCalls",
				map[string]any{"item": root.raw}, &raw); err != nil {
				warnings = append(warnings, "incoming call hierarchy was unavailable")
			} else {
				calls, callWarnings := parseCallRelations(c.workspace.Root, raw, "from", "incoming")
				warnings = append(warnings, callWarnings...)
				for _, caller := range calls {
					if !add(caller.item) {
						warnings = append(warnings,
							"call hierarchy nodes exceeded the graph limit")
						break
					}
					if len(edges) < MaxHierarchyEdges {
						edges = append(edges, GraphEdge{From: caller.item.ID, To: root.item.ID,
							Relationship: "calls"})
					}
				}
			}
		}
		if request.Direction == "outgoing" || request.Direction == "both" {
			var raw json.RawMessage
			if err := c.transport.request(ctx, "callHierarchy/outgoingCalls",
				map[string]any{"item": root.raw}, &raw); err != nil {
				warnings = append(warnings, "outgoing call hierarchy was unavailable")
			} else {
				calls, callWarnings := parseCallRelations(c.workspace.Root, raw, "to", "outgoing")
				warnings = append(warnings, callWarnings...)
				for _, callee := range calls {
					if !add(callee.item) {
						warnings = append(warnings,
							"call hierarchy nodes exceeded the graph limit")
						break
					}
					if len(edges) < MaxHierarchyEdges {
						edges = append(edges, GraphEdge{From: root.item.ID, To: callee.item.ID,
							Relationship: "calls"})
					}
				}
			}
		}
	}
	partial := len(warnings) > 0 || len(items) >= MaxResultItems ||
		len(edges) >= MaxHierarchyEdges
	if len(edges) >= MaxHierarchyEdges {
		warnings = append(warnings, "call hierarchy edges exceeded the graph limit")
	}
	return items, deduplicateEdges(edges), "", warnings, partial, nil
}

func (c *client) queryTypeHierarchy(ctx context.Context, request Request,
	document *documentBinding,
) ([]EvidenceItem, []GraphEdge, string, []string, bool, error) {
	var prepared json.RawMessage
	if err := c.transport.request(ctx, "textDocument/prepareTypeHierarchy",
		positionParams(document, request.Position), &prepared); err != nil {
		return nil, nil, "", nil, false, err
	}
	roots, warnings := parseHierarchyCandidates(c.workspace.Root, prepared, "type")
	if len(roots) > MaxHierarchyRoots {
		roots = roots[:MaxHierarchyRoots]
		warnings = append(warnings, "type hierarchy roots exceeded the expansion limit")
	}
	items := make([]EvidenceItem, 0)
	edges := make([]GraphEdge, 0)
	seen := make(map[string]struct{})
	add := func(item EvidenceItem) bool {
		if _, exists := seen[item.ID]; exists {
			return true
		}
		if len(items) >= MaxResultItems {
			return false
		}
		seen[item.ID] = struct{}{}
		items = append(items, item)
		return true
	}
	for _, root := range roots {
		root.item.Relationship = "root"
		root.item.ID = evidenceItemID(root.item)
		if !add(root.item) {
			warnings = append(warnings, "type hierarchy nodes exceeded the graph limit")
			break
		}
		if request.Direction == "supertypes" || request.Direction == "both" {
			var raw json.RawMessage
			if err := c.transport.request(ctx, "typeHierarchy/supertypes",
				map[string]any{"item": root.raw}, &raw); err != nil {
				warnings = append(warnings, "type supertypes were unavailable")
			} else {
				parents, relationWarnings := parseHierarchyCandidates(c.workspace.Root, raw, "type")
				warnings = append(warnings, relationWarnings...)
				for _, parent := range parents {
					parent.item.Relationship = "supertype"
					parent.item.ID = evidenceItemID(parent.item)
					if !add(parent.item) {
						warnings = append(warnings,
							"type hierarchy nodes exceeded the graph limit")
						break
					}
					if len(edges) < MaxHierarchyEdges {
						edges = append(edges, GraphEdge{From: root.item.ID, To: parent.item.ID,
							Relationship: "extends"})
					}
				}
			}
		}
		if request.Direction == "subtypes" || request.Direction == "both" {
			var raw json.RawMessage
			if err := c.transport.request(ctx, "typeHierarchy/subtypes",
				map[string]any{"item": root.raw}, &raw); err != nil {
				warnings = append(warnings, "type subtypes were unavailable")
			} else {
				children, relationWarnings := parseHierarchyCandidates(c.workspace.Root, raw, "type")
				warnings = append(warnings, relationWarnings...)
				for _, child := range children {
					child.item.Relationship = "subtype"
					child.item.ID = evidenceItemID(child.item)
					if !add(child.item) {
						warnings = append(warnings,
							"type hierarchy nodes exceeded the graph limit")
						break
					}
					if len(edges) < MaxHierarchyEdges {
						edges = append(edges, GraphEdge{From: child.item.ID, To: root.item.ID,
							Relationship: "extends"})
					}
				}
			}
		}
	}
	partial := len(warnings) > 0 || len(items) >= MaxResultItems ||
		len(edges) >= MaxHierarchyEdges
	if len(edges) >= MaxHierarchyEdges {
		warnings = append(warnings, "type hierarchy edges exceeded the graph limit")
	}
	return items, deduplicateEdges(edges), "", warnings, partial, nil
}

func parseCallRelations(root string, raw json.RawMessage, field, relationship string) (
	[]hierarchyCandidate, []string,
) {
	values := splitObjectOrArray(raw)
	result := make([]hierarchyCandidate, 0, min(len(values), MaxResultItems))
	warnings := make([]string, 0)
	for _, value := range values {
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil || len(object[field]) == 0 {
			warnings = append(warnings, "language server returned a malformed call relation")
			continue
		}
		candidates, candidateWarnings := parseHierarchyCandidates(root,
			json.RawMessage("["+string(object[field])+"]"), "call")
		warnings = append(warnings, candidateWarnings...)
		for _, candidate := range candidates {
			candidate.item.Relationship = relationship
			candidate.item.ID = evidenceItemID(candidate.item)
			result = append(result, candidate)
		}
	}
	return result, warnings
}

func parseHierarchyCandidates(root string, raw json.RawMessage, kind string) (
	[]hierarchyCandidate, []string,
) {
	values := splitObjectOrArray(raw)
	result := make([]hierarchyCandidate, 0, min(len(values), MaxResultItems))
	warnings := make([]string, 0)
	for index, value := range values {
		if index >= MaxResultItems {
			warnings = append(warnings, "hierarchy nodes exceeded the item limit")
			break
		}
		if len(value) > 64*1024 {
			warnings = append(warnings, "oversized hierarchy node was discarded")
			continue
		}
		var item struct {
			Name           string          `json:"name"`
			Kind           int             `json:"kind"`
			Tags           []int           `json:"tags,omitempty"`
			Detail         string          `json:"detail,omitempty"`
			URI            string          `json:"uri"`
			Range          Range           `json:"range"`
			SelectionRange Range           `json:"selectionRange"`
			Data           json.RawMessage `json:"data,omitempty"`
		}
		if json.Unmarshal(value, &item) != nil || item.Range.Validate() != nil ||
			item.SelectionRange.Validate() != nil || item.Kind < 1 || item.Kind > 26 {
			warnings = append(warnings, "invalid hierarchy node was discarded")
			continue
		}
		path, _, err := workspaceRelativeURI(root, item.URI)
		if err != nil {
			warnings = append(warnings, "hierarchy node outside the Workspace was discarded")
			continue
		}
		name, cleanedName := sanitizeText(item.Name, 1024, false)
		detail, cleanedDetail := sanitizeText(item.Detail, 4096, true)
		if name == "" {
			warnings = append(warnings, "empty hierarchy node was discarded")
			continue
		}
		rangeCopy := item.Range
		selectionCopy := item.SelectionRange
		evidence := EvidenceItem{Kind: kind + "_hierarchy", Name: name,
			Detail: detail, Path: path, Range: &rangeCopy, Selection: &selectionCopy,
			Metadata: map[string]string{"symbol_kind": strconv.Itoa(item.Kind)}}
		evidence.ID = evidenceItemID(evidence)
		result = append(result, hierarchyCandidate{raw: append(json.RawMessage(nil), value...),
			item: evidence})
		if cleanedName || cleanedDetail {
			warnings = append(warnings, fmt.Sprintf("%s hierarchy text was sanitized", kind))
		}
	}
	return result, warnings
}

func deduplicateEdges(values []GraphEdge) []GraphEdge {
	result := make([]GraphEdge, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, edge := range values {
		key := edge.From + "\x00" + edge.To + "\x00" + edge.Relationship
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, edge)
	}
	return result
}
