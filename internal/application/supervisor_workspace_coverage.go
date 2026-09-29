package application

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cyberagent-workbench/internal/domain"
)

type workspaceCoveragePage struct {
	call              domain.SupervisorToolCall
	page              map[string]any
	key               string
	index             int
	native            bool
	start, end, total int
	lines             []string
	valid             bool
	encodedBytes      int
}

type workspaceCoverageGroup struct {
	key       string
	pages     []workspaceCoveragePage
	needed    []workspaceCoveragePage
	newBytes  int
	hasNative bool
}

// Plan only within the already bounded lookup/current segment. Complete-file
// coverage is a selection of original pages, never a new concatenated body.
// Latest scope/path versions win; inconsistent same-version pages cannot make
// a complete group. The caller still owns the final atomic budget check.
func supervisorWorkspaceCoverage(receipts, native []domain.SupervisorToolCall) ([]workspaceCoverageGroup, map[int]bool) {
	pages := make([]workspaceCoveragePage, 0, len(receipts)+len(native))
	for _, source := range []struct {
		calls  []domain.SupervisorToolCall
		native bool
	}{{receipts, false}, {native, true}} {
		for index, call := range source.calls {
			page, key, ok := supervisorWorkspaceReadPage(call)
			if !ok {
				continue
			}
			p := workspaceCoveragePage{call: call, page: page, key: key, index: index, native: source.native,
				start: int(page["start_line"].(float64)), end: int(page["end_line"].(float64)), total: int(page["total_lines"].(float64))}
			p.lines = strings.Split(page["content"].(string), "\n")
			// Include repeated page headers/JSON escaping in the estimate: two
			// subpages can have one fewer body newline yet cost more than a full page.
			p.encodedBytes = len(stringMustMarshalCoverage(page))
			p.valid = workspaceCoveragePageConsistent(p)
			pages = append(pages, p)
		}
	}
	sort.SliceStable(pages, func(i, j int) bool {
		a, b := pages[i].call, pages[j].call
		if a.Turn != b.Turn {
			return a.Turn > b.Turn
		}
		if a.Round != b.Round {
			return a.Round > b.Round
		}
		return a.Position > b.Position
	})
	latest := map[string]string{}
	groups := map[string]*workspaceCoverageGroup{}
	invalid := map[string]bool{}
	for _, p := range pages {
		scope := stringMustMarshalCoverage([]any{p.page["workspace_id"], p.page["root_fingerprint"], p.page["path"]})
		version := p.page["content_sha256"].(string)
		if previous, exists := latest[scope]; exists && previous != version {
			continue
		}
		latest[scope] = version
		key := scope + "\x00" + version
		group := groups[key]
		if group == nil {
			group = &workspaceCoverageGroup{key: key}
			groups[key] = group
		}
		if !p.valid {
			invalid[key] = true
		}
		if len(group.pages) > 0 {
			first := group.pages[0]
			for _, field := range []string{"encoding", "newline", "total_lines", "total_bytes"} {
				if first.page[field] != p.page[field] {
					invalid[key] = true
				}
			}
			for _, prior := range group.pages {
				if !prior.valid || !p.valid {
					continue
				}
				for line := max(prior.start, p.start); line <= min(prior.end, p.end); line++ {
					if prior.lines[line-prior.start] != p.lines[line-p.start] {
						invalid[key] = true
					}
				}
			}
		}
		group.pages = append(group.pages, p)
		group.hasNative = group.hasNative || p.native
	}
	var result []workspaceCoverageGroup
	nativeComplete := map[int]bool{}
	for key, group := range groups {
		if invalid[key] {
			continue
		}
		needed, complete := workspaceCoverageIntervals(group.pages)
		if !complete {
			continue
		}
		if len(needed) == 0 {
			for _, page := range group.pages {
				if !page.native {
					nativeComplete[page.index] = true
				}
			}
			continue
		}
		group.needed = needed
		for _, page := range needed {
			group.newBytes += page.encodedBytes
		}
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].newBytes != result[j].newBytes {
			return result[i].newBytes < result[j].newBytes
		}
		if len(result[i].needed) != len(result[j].needed) {
			return len(result[i].needed) < len(result[j].needed)
		}
		return result[i].key < result[j].key
	})
	return result, nativeComplete
}

func stringMustMarshalCoverage(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func workspaceCoveragePageConsistent(p workspaceCoveragePage) bool {
	if p.page["redaction_count"].(float64) != 0 || len(p.lines) != p.end-p.start+1 {
		return false
	}
	encoding, newline := p.page["encoding"].(string), p.page["newline"].(string)
	if encoding != "utf-8" && encoding != "utf-8-bom" {
		return false
	}
	if newline != "lf" && newline != "crlf" && newline != "mixed" && newline != "none" {
		return false
	}
	var envelope supervisorToolResultEnvelope
	if json.Unmarshal([]byte(p.call.ResultJSON), &envelope) != nil {
		return false
	}
	for _, field := range []string{"start_line", "end_line", "total_lines", "total_bytes", "redaction_count", "truncated"} {
		if value, exists := envelope.Metadata[field]; exists && value != fmt.Sprint(p.page[field]) {
			return false
		}
	}
	if p.call.AuthorityJSON != "" {
		var authority map[string]any
		if json.Unmarshal([]byte(p.call.AuthorityJSON), &authority) != nil {
			return false
		}
		for _, field := range []string{"workspace_id", "root_fingerprint"} {
			if value, exists := authority[field]; exists && value != p.page[field] {
				return false
			}
		}
	}
	if p.call.PayloadJSON != "" {
		var payload map[string]any
		if json.Unmarshal([]byte(p.call.PayloadJSON), &payload) != nil {
			return false
		}
		if value, exists := payload["path"]; exists && value != p.page["path"] {
			return false
		}
	}
	return true
}

// Shortest path through page endpoints. Every state is the next missing line
// after exhausting native coverage, and every receipt edge moves forward.
// Memoizing those bounded endpoints avoids subset enumeration. A short page
// followed by free native coverage can beat a much larger overlapping page.
func workspaceCoverageIntervals(pages []workspaceCoveragePage) ([]workspaceCoveragePage, bool) {
	if len(pages) == 0 {
		return nil, false
	}
	total := pages[0].total
	closeNative := func(next int) int {
		for next <= total {
			end := next - 1
			for _, page := range pages {
				if page.native && page.start <= next && page.end >= next {
					end = max(end, page.end)
				}
			}
			if end < next {
				break
			}
			next = end + 1
		}
		return next
	}
	type path struct {
		pages    []workspaceCoveragePage
		bytes    int
		complete bool
	}
	memo := map[int]path{}
	var solve func(int) path
	solve = func(next int) path {
		next = closeNative(next)
		if next > total {
			return path{complete: true}
		}
		if known, exists := memo[next]; exists {
			return known
		}
		best := path{}
		for _, page := range pages {
			if page.native || page.start > next || page.end < next {
				continue
			}
			rest := solve(page.end + 1)
			if !rest.complete {
				continue
			}
			cost := page.encodedBytes + rest.bytes
			if !best.complete || cost < best.bytes || (cost == best.bytes && len(rest.pages)+1 < len(best.pages)) {
				best = path{pages: append([]workspaceCoveragePage{page}, rest.pages...), bytes: cost, complete: true}
			}
		}
		memo[next] = best
		return best
	}
	best := solve(1)
	return best.pages, best.complete
}
