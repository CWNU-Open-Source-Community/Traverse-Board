package application_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/toolgateway"
)

type observedSkillCatalogPage struct {
	Skills          []toolgateway.BuiltinSkillDescriptor `json:"skills"`
	Total           int                                  `json:"total"`
	Offset          int                                  `json:"offset"`
	Revision        string                               `json:"catalog_revision"`
	Next            *toolgateway.SkillReadRequest        `json:"next_request"`
	Diagnostic      string                               `json:"diagnostic"`
	CapabilityGrant bool                                 `json:"capability_grant"`
}

func pagingSkillFixture(t *testing.T, count int) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"paging-fixture","version":"1.0.0","description":"Paged Skill discovery fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for i := range count {
		name := fmt.Sprintf("skill-%03d", i)
		path := filepath.Join(directory, "skills", name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("---\nname: %s\ndescription: Inspect item %d\n---\nExact native body for item %d.\n", name, i, i)
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func skillPageResult(t *testing.T, request llm.ChatRequest, id string) observedSkillCatalogPage {
	t.Helper()
	// Supervisor replaces provider IDs with durable call identities. Read the
	// newest round's result; each discovery round in these tests has one call.
	for index := len(request.Messages) - 1; index >= 0; index-- {
		for _, result := range request.Messages[index].ToolResults {
			var envelope struct {
				Stdout    string `json:"stdout"`
				Truncated bool   `json:"truncated"`
			}
			if err := json.Unmarshal([]byte(result.Content), &envelope); err != nil || result.IsError || envelope.Truncated {
				t.Fatalf("bad page result: %s (%v)", result.Content, err)
			}
			var page observedSkillCatalogPage
			if err := json.Unmarshal([]byte(envelope.Stdout), &page); err != nil {
				t.Fatal(err)
			}
			if page.CapabilityGrant || len(page.Revision) != 64 {
				t.Fatalf("invalid page authority/revision: %+v", page)
			}
			return page
		}
	}
	t.Fatalf("missing page %q", id)
	return observedSkillCatalogPage{}
}

func TestPortableSkillPagingReachesBeyondInitialSummaryWithoutActivation(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "paging.db"))
	defer st.Close()
	directory := pagingSkillFixture(t, 33)
	value := importPortableFixture(t, st, directory, "paging-import")
	run := startedSkillRun(t, st)
	var chosen toolgateway.SkillReadRequest
	initial := map[string]bool{}
	p := &scriptedToolProvider{respond: func(request llm.ChatRequest, round int) (*llm.ChatResponse, error) {
		switch round {
		case 0:
			count := 0
			for _, tool := range request.Tools {
				if tool.Name != "skill_read" {
					continue
				}
				_, raw, _ := strings.Cut(tool.Description, " Available skills for this mode: ")
				var catalog []toolgateway.BuiltinSkillDescriptor
				if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
					t.Fatal(err)
				}
				for _, item := range catalog {
					if item.Portable() {
						count++
						initial[item.ComponentID] = true
					}
				}
			}
			if count != 32 {
				t.Fatalf("initial installed summary count=%d", count)
			}
			visible := false
			for _, message := range request.Messages {
				visible = visible || strings.Contains(message.Content, "32 of 33") && strings.Contains(message.Content, "Next skill_read request:")
			}
			if !visible {
				t.Fatal("partial discovery has no visible continuation")
			}
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{installedReadCall(toolgateway.SkillReadRequest{Catalog: true}, "first-page", "")}}, nil
		case 1:
			page := skillPageResult(t, request, "first-page")
			if page.Total != 33 || page.Offset != 0 || len(page.Skills) != 32 || page.Next == nil || page.Next.Offset != 32 || page.Next.CatalogRevision != page.Revision {
				t.Fatalf("bad first page: %+v", page)
			}
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{installedReadCall(*page.Next, "second-page", "")}}, nil
		case 2:
			page := skillPageResult(t, request, "second-page")
			if page.Total != 33 || page.Offset != 32 || len(page.Skills) != 1 || page.Next != nil {
				t.Fatalf("bad final page: %+v", page)
			}
			chosen = page.Skills[0].SkillReadRequest
			if chosen.InstallationID != value.ID || initial[chosen.ComponentID] {
				t.Fatalf("unexpected final component: %+v", chosen)
			}
			if reads, err := st.ListBuiltinSkillReadCalls(t.Context(), run.ID); err != nil || len(reads) != 0 {
				t.Fatalf("metadata activated instructions: %d %v", len(reads), err)
			}
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{installedReadCall(chosen, "read-last", "")}}, nil
		default:
			path := ""
			for _, skill := range value.Snapshot.Skills {
				if skill.Instructions.Component.ComponentID == chosen.ComponentID {
					path = skill.Instructions.Path
				}
			}
			if path == "" {
				t.Fatal("paged reference is not in the acquired inventory")
			}
			body, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(path)))
			if err != nil {
				t.Fatal(err)
			}
			assertInstalledReadResult(t, request, chosen, "", body)
			return textResponse(rootActionResponse(domain.RootActionContinue, "Paged native Skill read", "", "")), nil
		}
	}}
	if _, err := newToolLoopSupervisor(st, p).Step(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if len(p.Requests()) != 4 {
		t.Fatal("unexpected model retries", len(p.Requests()))
	}
	reads, err := st.ListBuiltinSkillReadCalls(t.Context(), run.ID)
	if err != nil || len(reads) != 1 {
		t.Fatalf("incorrect durable activation count: %d %v", len(reads), err)
	}
	var persisted toolgateway.SkillReadRequest
	if err := json.Unmarshal([]byte(reads[0].PayloadJSON), &persisted); err != nil || persisted != chosen {
		t.Fatal("metadata displaced native activation", persisted, err)
	}
}

func TestPortableSkillPagingRevisionAndContentRecheckAfterDisable(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "changed.db"))
	defer st.Close()
	value := importPortableFixture(t, st, pagingSkillFixture(t, 33), "changed-import")
	run := startedSkillRun(t, st)
	p := &scriptedToolProvider{respond: func(request llm.ChatRequest, round int) (*llm.ChatResponse, error) {
		switch round {
		case 0:
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{installedReadCall(toolgateway.SkillReadRequest{Catalog: true}, "before-disable", "")}}, nil
		case 1:
			page := skillPageResult(t, request, "before-disable")
			if page.Next == nil || len(page.Skills) != 32 {
				t.Fatal("missing bounded first page")
			}
			reviewInstalledFixture(t, st, value, plugins.ReviewDisable)
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{installedReadCall(*page.Next, "stale-page", ""), installedReadCall(page.Skills[0].SkillReadRequest, "stale-read", "")}}, nil
		default:
			if !hasToolResult(request, string(apperror.CodeConflict)) || !hasToolResult(request, string(apperror.CodePolicyDenied)) {
				t.Fatal("stale page or disabled content was accepted")
			}
			return textResponse(rootActionResponse(domain.RootActionContinue, "Changed catalog rejected", "", "")), nil
		}
	}}
	if _, err := newToolLoopSupervisor(st, p).Step(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if reads, err := st.ListBuiltinSkillReadCalls(t.Context(), run.ID); err != nil || len(reads) != 0 {
		t.Fatal("failed reads or discovery activated", len(reads), err)
	}
}
