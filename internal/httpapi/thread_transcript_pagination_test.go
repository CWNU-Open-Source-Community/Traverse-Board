package httpapi

import (
	"net/url"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/threadtranscript"
)

func TestThreadTranscriptCursorRestoresA10000ItemWindow(t *testing.T) {
	const path = "/api/v1/threads/thread-1/transcript"
	request, err := parseThreadTranscriptPage(url.Values{"limit": {"100"}}, path)
	if err != nil {
		t.Fatal(err)
	}
	for pageIndex := 0; pageIndex < 100; pageIndex++ {
		source := make([]threadtranscript.Source, 100)
		for index := range source {
			absolute := int64(10_000 - pageIndex*100 - index)
			source[index] = threadtranscript.Source{RunID: "run-1", Ordinal: 1,
				Sequence: absolute}
		}
		page := threadTranscriptPage(request, source, pageIndex < 99)
		if pageIndex == 99 {
			if page.NextCursor != "" || page.Truncated {
				t.Fatalf("final 10,000-item page is wrong: %#v", page)
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatalf("page %d omitted its recovery cursor", pageIndex)
		}
		request, err = parseThreadTranscriptPage(url.Values{
			"limit": {"100"}, "cursor": {page.NextCursor},
		}, path)
		if err != nil {
			t.Fatalf("page %d cursor did not restore: %v", pageIndex, err)
		}
	}
	if request.Consumed != 9_900 || request.BeforeOrdinal != 1 || request.BeforeSequence != 101 {
		t.Fatalf("10,000-item cursor recovery drifted: %#v", request)
	}
}

func TestThreadTranscriptCursorIsRouteScopedAndStrict(t *testing.T) {
	const path = "/api/v1/threads/thread-1/transcript"
	request, err := parseThreadTranscriptPage(url.Values{}, path)
	if err != nil {
		t.Fatal(err)
	}
	page := threadTranscriptPage(request, []threadtranscript.Source{{
		RunID: "run-1", Ordinal: 2, Sequence: 9,
	}}, true)
	for _, values := range []url.Values{
		{"cursor": {page.NextCursor}, "limit": {"1"}},
		{"cursor": {"not-a-cursor"}},
		{"cursor": {page.NextCursor, page.NextCursor}},
	} {
		resource := path
		if values.Get("limit") == "1" {
			resource = "/api/v1/threads/thread-2/transcript"
		}
		if _, err := parseThreadTranscriptPage(values, resource); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("invalid cursor values were accepted: values=%v code=%s err=%v",
				values, apperror.CodeOf(err), err)
		}
	}
}
