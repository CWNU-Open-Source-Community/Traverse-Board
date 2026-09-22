package webevidence

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	QuestionExcerptVersion    = "web_question_excerpt.v1"
	QuestionExcerptAlgorithm  = "lexical_window.v1"
	MaxQuestionRunes          = 1024
	MaxQuestionExcerptRunes   = 2048
	ExtractionCoverageSaved   = "saved_body_complete"
	ExtractionCoveragePartial = "saved_body_partial"
)

type Extraction struct {
	Version    string `json:"version"`
	Question   string `json:"question"`
	Algorithm  string `json:"algorithm"`
	SnapshotID string `json:"snapshot_id"`
	BodySHA256 string `json:"body_sha256"`
	SpanStart  int    `json:"span_start"`
	SpanEnd    int    `json:"span_end"`
	Matched    bool   `json:"matched"`
	Coverage   string `json:"coverage"`
}

func ExtractSnapshot(snapshot Snapshot, question string) (Extraction, error) {
	return ExtractSnapshotWithin(snapshot, question, MaxQuestionExcerptRunes)
}

func ExtractSnapshotWithin(snapshot Snapshot, question string, maxRunes int) (Extraction, error) {
	if maxRunes < 1 || maxRunes > MaxQuestionExcerptRunes {
		return Extraction{}, errors.New("web question extraction window must contain between 1 and 2048 runes")
	}
	if err := snapshot.Validate(); err != nil ||
		(snapshot.State != SourceFetched && snapshot.State != SourcePartial) {
		return Extraction{}, errors.New("question extraction requires a valid saved body snapshot")
	}
	question, err := NormalizeFetchQuestion(question)
	if err != nil {
		return Extraction{}, err
	}
	body := []rune(snapshot.Body)
	coverage := ExtractionCoverageSaved
	if snapshot.State == SourcePartial || snapshot.Truncated {
		coverage = ExtractionCoveragePartial
	}
	result := Extraction{Version: QuestionExcerptVersion, Question: question,
		Algorithm: QuestionExcerptAlgorithm, SnapshotID: snapshot.ID,
		BodySHA256: DigestBytes([]byte(snapshot.Body)), Coverage: coverage}
	if len(body) == 0 {
		return result, nil
	}
	window := min(maxRunes, len(body))
	tokens := extractionTokens(question)
	bestStart, bestScore := 0, 0
	lowerBody := []rune(strings.ToLower(snapshot.Body))
	step := max(1, window/2)
	for start := 0; start < len(body); start += step {
		end := min(len(body), start+window)
		candidate := string(lowerBody[start:end])
		score := extractionWindowScore(candidate, strings.ToLower(question), tokens)
		if score > bestScore {
			bestStart, bestScore = start, score
		}
		if end == len(body) {
			break
		}
	}
	result.SpanStart = bestStart
	result.SpanEnd = min(len(body), bestStart+window)
	result.Matched = bestScore > 0
	return result, result.Validate(snapshot)
}

func (e Extraction) Validate(snapshot Snapshot) error {
	normalizedQuestion, questionErr := NormalizeFetchQuestion(e.Question)
	expectedCoverage := ExtractionCoverageSaved
	if snapshot.State == SourcePartial || snapshot.Truncated {
		expectedCoverage = ExtractionCoveragePartial
	}
	if e.Version != QuestionExcerptVersion || e.Algorithm != QuestionExcerptAlgorithm ||
		e.SnapshotID != snapshot.ID || e.BodySHA256 != DigestBytes([]byte(snapshot.Body)) ||
		questionErr != nil || normalizedQuestion != e.Question ||
		e.SpanStart < 0 || e.SpanEnd < e.SpanStart ||
		e.SpanEnd > utf8.RuneCountInString(snapshot.Body) ||
		e.SpanEnd-e.SpanStart > MaxQuestionExcerptRunes ||
		e.Coverage != expectedCoverage {
		return errors.New("web question extraction is invalid")
	}
	return nil
}

func NormalizeFetchQuestion(question string) (string, error) {
	if !utf8.ValidString(question) {
		return "", errors.New("web fetch question must be valid UTF-8")
	}
	question = strings.Join(strings.Fields(strings.ReplaceAll(question, "\r\n", "\n")), " ")
	if question == "" || utf8.RuneCountInString(question) > MaxQuestionRunes {
		return "", errors.New("web fetch question must contain between 1 and 1024 runes")
	}
	return question, nil
}

func extractionTokens(question string) []string {
	question = strings.ToLower(question)
	seen := make(map[string]struct{})
	add := func(value string) {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	var word []rune
	var han []rune
	flushWord := func() {
		if len(word) > 0 {
			add(string(word))
			word = word[:0]
		}
	}
	flushHan := func() {
		for i := range han {
			add(string(han[i : i+1]))
			if i+1 < len(han) {
				add(string(han[i : i+2]))
			}
		}
		han = han[:0]
	}
	for _, current := range []rune(question) {
		if unicode.Is(unicode.Han, current) {
			flushWord()
			han = append(han, current)
			continue
		}
		flushHan()
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			word = append(word, current)
		} else {
			flushWord()
		}
	}
	flushWord()
	flushHan()
	result := make([]string, 0, len(seen))
	for token := range seen {
		result = append(result, token)
	}
	return result
}

func extractionWindowScore(candidate, phrase string, tokens []string) int {
	score := 0
	for _, token := range tokens {
		if strings.Contains(candidate, token) {
			score += 10 + min(16, utf8.RuneCountInString(token))
		}
	}
	if phrase != "" && strings.Contains(candidate, phrase) {
		score += 1000
	}
	return score
}
