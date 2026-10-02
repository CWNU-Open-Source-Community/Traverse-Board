package contextmgr

import (
	"errors"
	"strings"
	"testing"
)

func TestSelectSectionsReservesRequiredRulesAgainstHigherPriorityBackground(t *testing.T) {
	selection, err := SelectSections([]Section{
		{Kind: "summary", SourceID: "background", Content: strings.Repeat("s", 80), Priority: 1000},
		{Kind: "project_instruction", SourceID: "rule", Content: strings.Repeat("r", 80), Priority: 760, Required: true},
		{Kind: "project_instruction", SourceID: "old", Content: "withdrawn rule", Priority: 799, Excluded: true},
	}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Sections) != 1 || selection.Sections[0].SourceID != "rule" || len(selection.OmittedSources) != 2 {
		t.Fatalf("required delivery displaced: %#v", selection)
	}
	if selection.EstimatedTokens != EstimateTokens(strings.Repeat("r", 80)) || selection.EstimatedTokens > selection.TokenBudget {
		t.Fatal("included audit estimate does not match delivered content")
	}
	for _, omitted := range selection.OmittedSources {
		if (omitted.SourceID == "background" && omitted.OmissionReason != "budget") ||
			(omitted.SourceID == "old" && omitted.OmissionReason != "operator_excluded") {
			t.Fatal("omission reason lost")
		}
	}
}

func TestSelectSectionsStopsWhenCompleteRequiredSetCannotFit(t *testing.T) {
	_, err := SelectSections([]Section{
		{Kind: "rule", SourceID: "one", Content: strings.Repeat("r", 80), Priority: 1, Required: true},
		{Kind: "rule", SourceID: "two", Content: strings.Repeat("r", 80), Priority: 2, Required: true},
	}, 30)
	if !errors.Is(err, ErrRequiredContextBudget) {
		t.Fatalf("required set was silently partial: %v", err)
	}
	if _, err := SelectSections([]Section{{Kind: "rule", SourceID: "empty", Required: true}}, 10); err == nil {
		t.Fatal("empty mandatory rule accepted")
	}
}

func TestSelectSectionsLargeExcludedSourceStillValidatesIdentityAndFlags(t *testing.T) {
	excluded := Section{Kind: "rule", SourceID: "excluded", Content: strings.Repeat("x", MaxContextSectionBytes+1),
		Priority: 760, Excluded: true}
	for _, test := range []struct {
		name     string
		mutate   func(Section) []Section
		wantText string
	}{
		{"missing_kind", func(s Section) []Section { s.Kind = ""; return []Section{s} }, "kind and source id are required"},
		{"missing_id", func(s Section) []Section { s.SourceID = ""; return []Section{s} }, "kind and source id are required"},
		{"invalid_priority", func(s Section) []Section { s.Priority = 1001; return []Section{s} }, "outside 0..1000"},
		{"conflicting_flags", func(s Section) []Section { s.Required = true; return []Section{s} }, "required context section cannot be excluded"},
		{"duplicate_source", func(s Section) []Section { return []Section{s, s} }, "duplicate context source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := SelectSections(test.mutate(excluded), 10)
			if err == nil || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("excluded-source delivery-size exemption bypassed input validation: %v", err)
			}
		})
	}
}
