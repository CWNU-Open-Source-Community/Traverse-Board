package application_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

// Follow only continuation requests actually delivered to the model. Separate
// installations exercise paging across package boundaries and a third page;
// exact read pins must come from discovery, not an out-of-band fixture lookup.
func TestPortableSkillSummaryContinuationFindsAndReadsLaterInstallations(t *testing.T) {
	const total = 65
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "discovery.db"))
	defer st.Close()
	sources := t.TempDir()
	want := make(map[toolgateway.SkillReadRequest][]byte, total)
	for i := 1; i <= total; i++ {
		name := fmt.Sprintf("discovery-%02d", i)
		directory := filepath.Join(sources, name)
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		body := []byte(fmt.Sprintf("---\nname: %s\ndescription: Inspect discovery fixture %d.\n---\nACQUIRED_BODY_ONLY_%02d: exact retained instructions.\n", name, i, i))
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), body, 0600); err != nil {
			t.Fatal(err)
		}
		value := importPortableFixture(t, st, directory, name)
		want[installedReadPin(value)] = body
	}
	if len(want) != total {
		t.Fatalf("fixture identities=%d want=%d", len(want), total)
	}
	run := startedSkillRun(t, st)
	discovered := make(map[toolgateway.SkillReadRequest]bool, total)
	var chosen []toolgateway.SkillReadRequest
	var revision string
	acceptSummaries := func(items []toolgateway.BuiltinSkillDescriptor) {
		t.Helper()
		for _, item := range items {
			body, ok := want[item.SkillReadRequest]
			if !ok || discovered[item.SkillReadRequest] || item.ContentBytes != len(body) {
				t.Fatalf("summary substituted, duplicated or misdescribed an exact acquired identity: %+v", item)
			}
			discovered[item.SkillReadRequest] = true
		}
	}
	assertMetadataOnly := func(request llm.ChatRequest) {
		t.Helper()
		reads, err := st.ListBuiltinSkillReadCalls(t.Context(), run.ID)
		if err != nil || len(reads) != 0 {
			t.Fatalf("discovery activated instructions: reads=%d error=%v", len(reads), err)
		}
		raw, err := json.Marshal(request)
		if err != nil || strings.Contains(string(raw), "ACQUIRED_BODY_ONLY_") {
			t.Fatalf("summary discovery loaded instruction bodies: %v", err)
		}
	}
	provider := &scriptedToolProvider{respond: func(request llm.ChatRequest, round int) (*llm.ChatResponse, error) {
		switch round {
		case 0:
			var initial []toolgateway.BuiltinSkillDescriptor
			for _, tool := range request.Tools {
				if tool.Name != "skill_read" {
					continue
				}
				_, raw, ok := strings.Cut(tool.Description, " Available skills for this mode: ")
				if !ok {
					t.Fatal("initial Skill summaries are absent")
				}
				var catalog []toolgateway.BuiltinSkillDescriptor
				if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
					t.Fatal(err)
				}
				for _, item := range catalog {
					if item.Portable() {
						initial = append(initial, item)
					}
				}
			}
			if len(initial) != 32 {
				t.Fatalf("initial installed summaries=%d want=32", len(initial))
			}
			acceptSummaries(initial)
			var continuation toolgateway.SkillReadRequest
			for _, message := range request.Messages {
				if message.Role != "system" || !strings.Contains(message.Content, "32 of 65") {
					continue
				}
				_, raw, ok := strings.Cut(message.Content, "Next skill_read request: ")
				if ok {
					if err := json.Unmarshal([]byte(raw), &continuation); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !continuation.Catalog || continuation.Offset != 32 || len(continuation.CatalogRevision) != 64 {
				t.Fatalf("partial first screen did not supply a usable continuation: %+v", continuation)
			}
			revision = continuation.CatalogRevision
			assertMetadataOnly(request)
			t.Logf("initial summaries=%d total=%d next offset=%d; activation count=0", len(initial), total, continuation.Offset)
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{installedReadCall(continuation, "discover-second-page", "")}}, nil
		case 1:
			page := skillPageResult(t, request, "second-page")
			if page.Total != total || page.Offset != 32 || len(page.Skills) != 32 || page.Revision != revision ||
				page.Next == nil || !page.Next.Catalog || page.Next.Offset != 64 || page.Next.CatalogRevision != revision {
				t.Fatalf("second page cannot continue to later Skills: %+v", page)
			}
			acceptSummaries(page.Skills)
			chosen = append(chosen, page.Skills[0].SkillReadRequest, page.Skills[31].SkillReadRequest)
			assertMetadataOnly(request)
			t.Logf("second page offset=%d summaries=%d next offset=%d; activation count=0", page.Offset, len(page.Skills), page.Next.Offset)
			return &llm.ChatResponse{ToolCalls: []llm.ToolCall{installedReadCall(*page.Next, "discover-third-page", "")}}, nil
		case 2:
			page := skillPageResult(t, request, "third-page")
			if page.Total != total || page.Offset != 64 || len(page.Skills) != 1 || page.Revision != revision || page.Next != nil {
				t.Fatalf("third page did not terminate with the remaining Skill: %+v", page)
			}
			acceptSummaries(page.Skills)
			chosen = append(chosen, page.Skills[0].SkillReadRequest)
			if len(discovered) != len(want) {
				t.Fatalf("silent discovery omission: unique summaries=%d installations=%d", len(discovered), len(want))
			}
			assertMetadataOnly(request)
			t.Logf("third page offset=%d summaries=%d; all %d identities discovered exactly once; activation count=0", page.Offset, len(page.Skills), len(discovered))
			calls := make([]llm.ToolCall, 0, len(chosen))
			for index, pin := range chosen {
				calls = append(calls, installedReadCall(pin, fmt.Sprintf("read-later-%d", index), ""))
			}
			return &llm.ChatResponse{ToolCalls: calls}, nil
		case 3:
			for index, pin := range chosen {
				assertInstalledReadResult(t, request, pin, "", want[pin])
				t.Logf("catalog ordinal=%d exact skill_read delivered %d original bytes with matching digests", []int{33, 64, 65}[index], len(want[pin]))
			}
			return textResponse(rootActionResponse(domain.RootActionContinue, "Discovered and read Skills beyond the first screen", "", "")), nil
		default:
			t.Fatalf("unexpected provider retry/round=%d", round)
			return nil, nil
		}
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	persistedRun, err := st.GetRun(t.Context(), run.ID)
	if err != nil || len(provider.Requests()) != 4 || result.RequestedAction != domain.RootActionContinue || persistedRun.Status != domain.RunRunning {
		t.Fatalf("discovery/read Run did not progress: requests=%d run=%s result=%+v error=%v", len(provider.Requests()), persistedRun.Status, result, err)
	}
	reads, err := st.ListBuiltinSkillReadCalls(t.Context(), run.ID)
	if err != nil || len(reads) != len(chosen) {
		t.Fatalf("metadata consumed activation or content reads were lost: activations=%d selected=%d error=%v", len(reads), len(chosen), err)
	}
	persisted := make(map[toolgateway.SkillReadRequest]bool, len(chosen))
	for _, call := range reads {
		var pin toolgateway.SkillReadRequest
		if err := json.Unmarshal([]byte(call.PayloadJSON), &pin); err != nil || pin.Catalog || persisted[pin] {
			t.Fatalf("invalid or duplicate durable activation: %+v error=%v", pin, err)
		}
		persisted[pin] = true
	}
	for _, pin := range chosen {
		if !persisted[pin] {
			t.Fatalf("precisely read component missing from activation ledger: %+v", pin)
		}
	}
	t.Logf("provider requests=%d durable content activations=%d run=%s checkpoint=%s", len(provider.Requests()), len(reads), persistedRun.Status, result.Checkpoint.Phase)
}
