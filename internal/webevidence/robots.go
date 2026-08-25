package webevidence

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

type RobotsDecision struct {
	Allowed bool
	State   string
}

func CheckRobots(ctx context.Context, client *SafeHTTPClient,
	canonicalURL string,
) (RobotsDecision, error) {
	return checkRobots(ctx, client, canonicalURL, nil)
}

func CheckRobotsAuthorized(ctx context.Context, client *SafeHTTPClient,
	canonicalURL string, authority NetworkAuthority,
) (RobotsDecision, error) {
	return checkRobots(ctx, client, canonicalURL, func(raw string) error {
		_, err := authority.Authorize(raw)
		return err
	})
}

func checkRobots(ctx context.Context, client *SafeHTTPClient,
	canonicalURL string, authorize func(string) error,
) (RobotsDecision, error) {
	canonicalURL, err := CanonicalizePublicHTTPSURL(canonicalURL)
	if err != nil {
		return RobotsDecision{}, err
	}
	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		return RobotsDecision{}, err
	}
	robotsURL := &url.URL{Scheme: "https", Host: parsed.Host, Path: "/robots.txt"}
	document, err := client.GetAuthorized(ctx, robotsURL.String(), 256*1024, "text/plain",
		authorize)
	if err != nil {
		return RobotsDecision{State: "unknown"}, errors.New("robots policy could not be verified")
	}
	switch document.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return RobotsDecision{Allowed: true, State: "not_present"}, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return RobotsDecision{State: "blocked"}, nil
	}
	if document.StatusCode < 200 || document.StatusCode >= 300 {
		return RobotsDecision{State: "unknown"}, errors.New("robots policy returned an indeterminate status")
	}
	if document.Truncated {
		return RobotsDecision{State: "unknown"}, errors.New("robots policy exceeded the configured limit")
	}
	mediaType, _, contentTypeErr := mime.ParseMediaType(document.Header.Get("Content-Type"))
	if contentTypeErr != nil || !strings.EqualFold(mediaType, "text/plain") ||
		!utf8.Valid(document.Body) {
		return RobotsDecision{State: "unknown"}, errors.New("robots policy encoding or MIME is invalid")
	}
	target := parsed.EscapedPath()
	if parsed.RawQuery != "" {
		target += "?" + parsed.RawQuery
	}
	allowed := robotsAllows(string(document.Body), target)
	state := "allowed"
	if !allowed {
		state = "blocked"
	}
	return RobotsDecision{Allowed: allowed, State: state}, nil
}

type robotsRule struct {
	allow   bool
	pattern string
}

type robotsGroup struct {
	agents []string
	rules  []robotsRule
}

const (
	maxRobotsRules        = 4096
	maxRobotsPatternBytes = 2048
)

func robotsAllows(content, escapedPath string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	groups := make([]robotsGroup, 0)
	var currentAgents []string
	var currentRules []robotsRule
	ruleCount := 0
	flush := func() {
		if len(currentAgents) > 0 {
			groups = append(groups, robotsGroup{
				agents: append([]string(nil), currentAgents...),
				rules:  append([]robotsRule(nil), currentRules...),
			})
		}
		currentAgents = nil
		currentRules = nil
	}
	seenRule := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if seenRule {
				flush()
				seenRule = false
			}
			if agent := normalizedRobotsAgent(value); agent != "" {
				currentAgents = append(currentAgents, agent)
			}
		case "allow", "disallow":
			seenRule = true
			if value != "" {
				if len([]byte(value)) > maxRobotsPatternBytes || ruleCount >= maxRobotsRules {
					return false
				}
				currentRules = append(currentRules, robotsRule{allow: key == "allow", pattern: value})
				ruleCount++
			}
		}
	}
	flush()
	bestAgentLength := -1
	selected := make([]robotsRule, 0)
	for _, group := range groups {
		matchLength := robotsGroupMatchLength(group.agents)
		if matchLength < 0 || matchLength < bestAgentLength {
			continue
		}
		if matchLength > bestAgentLength {
			bestAgentLength = matchLength
			selected = selected[:0]
		}
		selected = append(selected, group.rules...)
	}
	bestLength := -1
	allowed := true
	for _, rule := range selected {
		matched, specificity := robotsPatternMatches(rule.pattern, escapedPath)
		if matched && specificity >= bestLength {
			if specificity > bestLength || rule.allow {
				bestLength, allowed = specificity, rule.allow
			}
		}
	}
	return allowed
}

const robotsProductToken = "traverse-board-webevidence"

func normalizedRobotsAgent(value string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSuffix(fields[0], "/")
}

func robotsGroupMatchLength(agents []string) int {
	best := -1
	for _, agent := range agents {
		switch {
		case agent == "*" && best < 0:
			best = 0
		case agent != "" && agent != "*" && strings.HasPrefix(robotsProductToken, agent) &&
			len(agent) > best:
			best = len(agent)
		}
	}
	return best
}

// robotsPatternMatches implements the robots exclusion '*' wildcard and a
// terminal '$' anchor without compiling attacker-controlled regular
// expressions. A non-anchored rule is a path-prefix match.
func robotsPatternMatches(pattern, escapedPath string) (bool, int) {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}
	specificity := len(strings.ReplaceAll(pattern, "*", ""))
	startsWildcard := strings.HasPrefix(pattern, "*")
	endsWildcard := strings.HasSuffix(pattern, "*")
	rawSegments := strings.Split(pattern, "*")
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return true, specificity
	}
	position := 0
	for index, segment := range segments {
		first := index == 0
		last := index == len(segments)-1
		switch {
		case first && !startsWildcard:
			if !strings.HasPrefix(escapedPath, segment) {
				return false, specificity
			}
			position = len(segment)
		case last && anchored && !endsWildcard:
			start := len(escapedPath) - len(segment)
			if start < position || start < 0 || escapedPath[start:] != segment {
				return false, specificity
			}
			position = len(escapedPath)
		default:
			offset := strings.Index(escapedPath[position:], segment)
			if offset < 0 {
				return false, specificity
			}
			position += offset + len(segment)
		}
	}
	return !anchored || endsWildcard || position == len(escapedPath), specificity
}
