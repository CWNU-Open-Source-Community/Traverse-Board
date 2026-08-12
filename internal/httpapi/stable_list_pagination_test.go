package httpapi

import (
	"encoding/base64"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

func TestStableListCursorRoundTripsExactRouteFilterAndNanoseconds(t *testing.T) {
	path := "/api/v1/runs"
	values := url.Values{"limit": {"25"}, "status": {"running"}}
	initial, err := parseStableListPage(values, path)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Limit != 25 || initial.Anchor != (stableListPageAnchor{}) {
		t.Fatalf("initial stable list page diverged: %#v", initial)
	}
	at := time.Date(2026, time.August, 12, 4, 5, 6, 123456789, time.UTC)
	items, page := trimStableListPage([]RunView{
		{ID: "run-2", CreatedAt: at.Add(time.Second)},
		{ID: "run-1", CreatedAt: at},
		{ID: "run-0", CreatedAt: at.Add(-time.Second)},
	}, stableListPageRequest{Limit: 2, Scope: initial.Scope}, runStableListPosition)
	if len(items) != 2 || page.NextCursor == "" || page.Truncated {
		t.Fatalf("stable list cursor was not issued: items=%#v page=%#v", items, page)
	}
	continued, err := parseStableListPage(url.Values{
		"limit": {"10"}, "status": {"running"}, "cursor": {page.NextCursor},
	}, path)
	if err != nil {
		t.Fatal(err)
	}
	if continued.Limit != 10 || continued.Scope != initial.Scope ||
		continued.Anchor.BeforeCreatedAt != at || continued.Anchor.BeforeID != "run-1" ||
		continued.Anchor.Consumed != 2 {
		t.Fatalf("stable list cursor lost its exact keyset: %#v", continued)
	}
	for _, changed := range []url.Values{
		{"cursor": {page.NextCursor}, "status": {"paused"}},
		{"cursor": {page.NextCursor}, "mission_id": {"mission-1"}},
	} {
		if _, err := parseStableListPage(changed, path); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("changed-filter cursor code=%s err=%v values=%v", apperror.CodeOf(err), err, changed)
		}
	}
	if _, err := parseStableListPage(url.Values{"cursor": {page.NextCursor}},
		"/api/v1/sessions"); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("cross-route cursor code=%s err=%v", apperror.CodeOf(err), err)
	}
}

func TestStableListCursorRejectsMalformedOrWidenedState(t *testing.T) {
	path := "/api/v1/sessions"
	scope := pageScope(path, url.Values{})
	cases := []string{
		"not-base64!",
		base64.RawURLEncoding.EncodeToString([]byte(
			`{"v":2,"s":"` + scope + `","t":"2026-08-12T00:00:00Z","i":"session-1","c":1,"x":true}`)),
		base64.RawURLEncoding.EncodeToString([]byte(
			`{"v":2,"s":"` + scope + `","t":"2026-08-12T00:00:00+00:00","i":"session-1","c":1}`)),
		base64.RawURLEncoding.EncodeToString([]byte(
			`{"v":2,"s":"` + scope + `","t":"2026-08-12T00:00:00Z","i":"session-1","c":100000}`)),
		base64.RawURLEncoding.EncodeToString([]byte(
			`{"v":2,"s":"` + scope + `","t":"2026-08-12T00:00:00Z","i":"bad/session","c":1}`)),
		base64.RawURLEncoding.EncodeToString([]byte(
			`{"v":2,"s":"` + scope + `","t":"2026-08-12T00:00:00Z","i":"session-1","c":1} trailing`)),
		strings.Repeat("a", MaxCursorBytes+1),
	}
	for _, encoded := range cases {
		if _, err := parseStableListPage(url.Values{"cursor": {encoded}}, path); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("malformed cursor code=%s err=%v cursor=%q", apperror.CodeOf(err), err, encoded)
		}
	}
}

func TestStableListPageStopsAtBoundedReadWindow(t *testing.T) {
	request := stableListPageRequest{
		Limit: 100, Scope: strings.Repeat("a", 32),
		Anchor: stableListPageAnchor{Consumed: maxStoreCursorOffset - 1},
	}
	items, page := trimStableListPage([]SessionView{
		{ID: "session-last", CreatedAt: time.Now().UTC()},
		{ID: "session-beyond", CreatedAt: time.Now().UTC().Add(-time.Second)},
	}, request, sessionStableListPosition)
	if len(items) != 1 || page.NextCursor != "" || !page.Truncated || page.Limit != request.Limit {
		t.Fatalf("stable list page widened its read window: items=%#v page=%#v", items, page)
	}
}

func TestStableListPagesDoNotAliasCallerBackingArrays(t *testing.T) {
	at := time.Now().UTC()
	runs := []RunView{
		{ID: "run-a", CreatedAt: at},
		{ID: "run-b", CreatedAt: at.Add(-time.Second)},
		{ID: "run-c", CreatedAt: at.Add(-2 * time.Second)},
	}
	pageItems, page := trimStableListPage(runs, stableListPageRequest{
		Limit: 2, Scope: strings.Repeat("a", 32),
	}, runStableListPosition)
	if len(pageItems) != 2 || page.NextCursor == "" {
		t.Fatalf("unexpected page: items=%#v page=%#v", pageItems, page)
	}
	pageItems[0].ID = "changed"
	if runs[0].ID != "run-a" {
		t.Fatalf("page mutation changed Store-owned input: %#v", runs)
	}
	empty, emptyPage := trimStableListPage(make([]RunView, 0), stableListPageRequest{
		Limit: 2, Scope: strings.Repeat("a", 32),
	}, runStableListPosition)
	if empty == nil || len(empty) != 0 || emptyPage.NextCursor != "" || emptyPage.Truncated {
		t.Fatalf("empty collection lost its array shape: items=%#v page=%#v", empty, emptyPage)
	}
}

func TestRunAndSessionContinuationPagesSurviveUpdatesAndLaterInserts(t *testing.T) {
	fixture := newAPIFixture(t)
	ctx := t.Context()
	runService := application.NewRunService(fixture.store)
	for index := 0; index < 4; index++ {
		if _, _, err := runService.Create(ctx, application.CreateRunRequest{
			Goal: "stable page Run " + string(rune('A'+index)), Profile: "review",
			ModelRoute: "review", Budget: domain.Budget{MaxTurns: 2, MaxToolCalls: 2},
		}); err != nil {
			t.Fatal(err)
		}
	}
	runFirst := fixture.get(t, "/api/v1/runs?limit=2")
	var runFirstItems []RunView
	runFirstEnvelope := decodeData(t, runFirst, &runFirstItems)
	if len(runFirstItems) != 2 || runFirstEnvelope.Page == nil || runFirstEnvelope.Page.NextCursor == "" {
		t.Fatalf("Run first page is incomplete: items=%#v page=%#v", runFirstItems, runFirstEnvelope.Page)
	}
	remainingRuns, err := fixture.store.ListRunsByCreationPage(ctx,
		domain.RunFilter{Limit: 100}, runFirstItems[1].CreatedAt, runFirstItems[1].ID)
	if err != nil || len(remainingRuns) < 2 {
		t.Fatalf("Run continuation fixture is incomplete: runs=%#v err=%v", remainingRuns, err)
	}
	if _, err := runService.Start(ctx, remainingRuns[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runService.Create(ctx, application.CreateRunRequest{
		Goal: "Run created after first page", Profile: "review", ModelRoute: "review",
		Budget: domain.Budget{MaxTurns: 2, MaxToolCalls: 2},
	}); err != nil {
		t.Fatal(err)
	}
	runSecond := fixture.get(t, "/api/v1/runs?limit=2&cursor="+
		url.QueryEscape(runFirstEnvelope.Page.NextCursor))
	var runSecondItems []RunView
	decodeData(t, runSecond, &runSecondItems)
	if got, want := viewRunIDs(runSecondItems), []string{remainingRuns[0].ID, remainingRuns[1].ID}; !slices.Equal(got, want) {
		t.Fatalf("Run continuation shifted after update/insert: got=%v want=%v", got, want)
	}

	base := time.Now().UTC().Add(-24 * time.Hour)
	for index := 0; index < 5; index++ {
		record := session.New("", "stable page Session", "review")
		record.ID = "session-stable-" + string(rune('a'+index))
		record.CreatedAt = base.Add(time.Duration(index) * time.Second)
		record.UpdatedAt = record.CreatedAt
		if err := fixture.store.SaveSession(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	sessionFirst := fixture.get(t, "/api/v1/sessions?limit=2")
	var sessionFirstItems []SessionView
	sessionFirstEnvelope := decodeData(t, sessionFirst, &sessionFirstItems)
	if len(sessionFirstItems) != 2 || sessionFirstEnvelope.Page == nil || sessionFirstEnvelope.Page.NextCursor == "" {
		t.Fatalf("Session first page is incomplete: items=%#v page=%#v", sessionFirstItems, sessionFirstEnvelope.Page)
	}
	remainingSessions, err := fixture.store.ListSessionsByCreationPage(ctx,
		sessionFirstItems[1].CreatedAt, sessionFirstItems[1].ID, 100)
	if err != nil || len(remainingSessions) < 2 {
		t.Fatalf("Session continuation fixture is incomplete: sessions=%#v err=%v", remainingSessions, err)
	}
	message := session.NewMessage(remainingSessions[0].ID, "user", "update older Session")
	if _, err := fixture.store.SaveSessionMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	later := session.New("", "Session created after first page", "review")
	if err := fixture.store.SaveSession(ctx, later); err != nil {
		t.Fatal(err)
	}
	sessionSecond := fixture.get(t, "/api/v1/sessions?limit=2&cursor="+
		url.QueryEscape(sessionFirstEnvelope.Page.NextCursor))
	var sessionSecondItems []SessionView
	decodeData(t, sessionSecond, &sessionSecondItems)
	if got, want := viewSessionIDs(sessionSecondItems),
		[]string{remainingSessions[0].ID, remainingSessions[1].ID}; !slices.Equal(got, want) {
		t.Fatalf("Session continuation shifted after update/insert: got=%v want=%v", got, want)
	}
}

func TestCreationPagePreservesSubMillisecondOrderingAndIdentityTieBreak(t *testing.T) {
	fixture := newAPIFixture(t)
	ctx := t.Context()
	base := time.Date(2026, time.August, 12, 10, 0, 0, 123000000, time.UTC)
	for _, record := range []session.Session{
		{ID: "session-nano-a", Title: "nanosecond A", Route: "review", Status: session.StatusActive,
			CreatedAt: base.Add(100 * time.Nanosecond), UpdatedAt: base.Add(100 * time.Nanosecond)},
		{ID: "session-nano-b", Title: "nanosecond B", Route: "review", Status: session.StatusActive,
			CreatedAt: base.Add(200 * time.Nanosecond), UpdatedAt: base.Add(200 * time.Nanosecond)},
		{ID: "session-tie-z", Title: "tie Z", Route: "review", Status: session.StatusActive,
			CreatedAt: base, UpdatedAt: base},
		{ID: "session-tie-y", Title: "tie Y", Route: "review", Status: session.StatusActive,
			CreatedAt: base, UpdatedAt: base},
	} {
		if err := fixture.store.SaveSession(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	first, err := fixture.store.ListSessionsByCreationPage(ctx, time.Time{}, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var relevant []string
	for _, record := range first {
		if strings.HasPrefix(record.ID, "session-nano-") || strings.HasPrefix(record.ID, "session-tie-") {
			relevant = append(relevant, record.ID)
		}
	}
	want := []string{"session-nano-b", "session-nano-a", "session-tie-z", "session-tie-y"}
	if !slices.Equal(relevant, want) {
		t.Fatalf("creation ordering lost nanoseconds or identity tie-break: got=%v want=%v", relevant, want)
	}
	continued, err := fixture.store.ListSessionsByCreationPage(ctx, base, "session-tie-z", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(continued) == 0 || continued[0].ID != "session-tie-y" {
		t.Fatalf("creation continuation lost an equal-time identity: %#v", continued)
	}
}

func viewRunIDs(items []RunView) []string {
	ids := make([]string, len(items))
	for index := range items {
		ids[index] = items[index].ID
	}
	return ids
}

func viewSessionIDs(items []SessionView) []string {
	ids := make([]string, len(items))
	for index := range items {
		ids[index] = items[index].ID
	}
	return ids
}
