package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/threadtranscript"
)

const threadTranscriptCursorVersion = 1

type threadTranscriptPageRequest struct {
	Limit          int
	Scope          string
	BeforeOrdinal  int64
	BeforeSequence int64
	Consumed       int
}

type threadTranscriptCursor struct {
	Version        int    `json:"v"`
	Scope          string `json:"s"`
	BeforeOrdinal  int64  `json:"o"`
	BeforeSequence int64  `json:"q"`
	Consumed       int    `json:"c"`
}

func parseThreadTranscriptPage(values url.Values, resourcePath string) (threadTranscriptPageRequest, error) {
	request := threadTranscriptPageRequest{Limit: DefaultPageLimit,
		Scope: pageScope(resourcePath, values)}
	if raw, ok := singleQueryValue(values, "limit"); ok {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > MaxPageLimit {
			return threadTranscriptPageRequest{}, apperror.New(apperror.CodeInvalidArgument,
				"thread transcript limit must be between 1 and 100")
		}
		request.Limit = parsed
	}
	if raw, ok := singleQueryValue(values, "cursor"); ok {
		cursor, err := decodeThreadTranscriptCursor(raw, request.Scope)
		if err != nil {
			return threadTranscriptPageRequest{}, err
		}
		request.BeforeOrdinal = cursor.BeforeOrdinal
		request.BeforeSequence = cursor.BeforeSequence
		request.Consumed = cursor.Consumed
	}
	return request, nil
}

func decodeThreadTranscriptCursor(encoded, expectedScope string) (threadTranscriptCursor, error) {
	invalid := func() (threadTranscriptCursor, error) {
		return threadTranscriptCursor{}, apperror.New(apperror.CodeInvalidArgument,
			"thread transcript cursor is invalid")
	}
	if len(encoded) == 0 || len(encoded) > MaxCursorBytes {
		return invalid()
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > MaxCursorBytes {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor threadTranscriptCursor
	if err := decoder.Decode(&cursor); err != nil {
		return invalid()
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invalid()
	}
	if cursor.Version != threadTranscriptCursorVersion || cursor.Scope != expectedScope ||
		cursor.BeforeOrdinal <= 0 || cursor.BeforeSequence < 0 || cursor.Consumed <= 0 ||
		cursor.Consumed >= maxStoreCursorOffset {
		return threadTranscriptCursor{}, apperror.New(apperror.CodeInvalidArgument,
			"thread transcript cursor does not match this Thread")
	}
	return cursor, nil
}

func threadTranscriptPage(request threadTranscriptPageRequest,
	source []threadtranscript.Source, hasMore bool,
) *Page {
	page := &Page{Limit: request.Limit}
	if !hasMore || len(source) == 0 {
		return page
	}
	consumed := request.Consumed + len(source)
	if consumed >= maxStoreCursorOffset {
		page.Truncated = true
		return page
	}
	oldest := source[len(source)-1]
	raw, _ := json.Marshal(threadTranscriptCursor{Version: threadTranscriptCursorVersion,
		Scope: request.Scope, BeforeOrdinal: oldest.Ordinal,
		BeforeSequence: oldest.Sequence, Consumed: consumed})
	page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	return page
}
