package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	"cyberagent-workbench/internal/apperror"
)

const stableListCursorVersion = 2

type stableListPageAnchor struct {
	BeforeCreatedAt time.Time
	BeforeID        string
	Consumed        int
}

type stableListPageRequest struct {
	Limit  int
	Scope  string
	Anchor stableListPageAnchor
}

type stableListCursor struct {
	Version         int    `json:"v"`
	Scope           string `json:"s"`
	BeforeCreatedAt string `json:"t"`
	BeforeID        string `json:"i"`
	Consumed        int    `json:"c"`
}

func parseStableListPage(values url.Values, resourcePath string) (stableListPageRequest, error) {
	limit := DefaultPageLimit
	if raw, ok := singleQueryValue(values, "limit"); ok {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > MaxPageLimit {
			return stableListPageRequest{}, apperror.New(apperror.CodeInvalidArgument,
				fmt.Sprintf("page limit must be between 1 and %d", MaxPageLimit))
		}
		limit = parsed
	}
	request := stableListPageRequest{Limit: limit, Scope: pageScope(resourcePath, values)}
	if raw, ok := singleQueryValue(values, "cursor"); ok {
		anchor, err := decodeStableListCursor(raw, request.Scope)
		if err != nil {
			return stableListPageRequest{}, err
		}
		request.Anchor = anchor
	}
	return request, nil
}

func decodeStableListCursor(encoded string, expectedScope string) (stableListPageAnchor, error) {
	invalid := func(message string) (stableListPageAnchor, error) {
		return stableListPageAnchor{}, apperror.New(apperror.CodeInvalidArgument, message)
	}
	if len(encoded) == 0 || len(encoded) > MaxCursorBytes {
		return invalid("stable list cursor is invalid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > MaxCursorBytes {
		return invalid("stable list cursor is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor stableListCursor
	if err := decoder.Decode(&cursor); err != nil {
		return invalid("stable list cursor is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invalid("stable list cursor is invalid")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.BeforeCreatedAt)
	if err != nil || createdAt.IsZero() || createdAt.UTC().Format(time.RFC3339Nano) != cursor.BeforeCreatedAt ||
		validateIdentity(cursor.BeforeID, "stable list cursor identity") != nil ||
		cursor.Version != stableListCursorVersion || cursor.Scope != expectedScope ||
		cursor.Consumed <= 0 || cursor.Consumed >= maxStoreCursorOffset {
		return invalid("stable list cursor does not match this resource and filter")
	}
	return stableListPageAnchor{
		BeforeCreatedAt: createdAt.UTC(), BeforeID: cursor.BeforeID, Consumed: cursor.Consumed,
	}, nil
}

func encodeStableListCursor(scope string, anchor stableListPageAnchor) (string, bool) {
	if anchor.BeforeCreatedAt.IsZero() || validateIdentity(anchor.BeforeID, "stable list cursor identity") != nil ||
		anchor.Consumed <= 0 || anchor.Consumed >= maxStoreCursorOffset {
		return "", false
	}
	raw, err := json.Marshal(stableListCursor{
		Version: stableListCursorVersion, Scope: scope,
		BeforeCreatedAt: anchor.BeforeCreatedAt.UTC().Format(time.RFC3339Nano),
		BeforeID:        anchor.BeforeID, Consumed: anchor.Consumed,
	})
	if err != nil || len(raw) == 0 || len(raw) > MaxCursorBytes {
		return "", false
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded, len(encoded) <= MaxCursorBytes
}

func stableListStoreLimit(request stableListPageRequest) int {
	remaining := maxStoreCursorOffset - request.Anchor.Consumed
	if remaining > request.Limit {
		remaining = request.Limit
	}
	return remaining + 1
}

func trimStableListPage[T any](items []T, request stableListPageRequest,
	position func(T) (time.Time, string),
) ([]T, *Page) {
	remaining := maxStoreCursorOffset - request.Anchor.Consumed
	allowed := request.Limit
	if allowed > remaining {
		allowed = remaining
	}
	hasMore := len(items) > allowed
	if hasMore {
		items = items[:allowed]
	}
	cloned := make([]T, len(items))
	copy(cloned, items)
	items = cloned
	page := &Page{Limit: request.Limit}
	if !hasMore {
		return items, page
	}
	nextConsumed := request.Anchor.Consumed + len(items)
	if len(items) == 0 || nextConsumed >= maxStoreCursorOffset {
		page.Truncated = true
		return items, page
	}
	createdAt, id := position(items[len(items)-1])
	encoded, ok := encodeStableListCursor(request.Scope, stableListPageAnchor{
		BeforeCreatedAt: createdAt, BeforeID: id, Consumed: nextConsumed,
	})
	if !ok {
		page.Truncated = true
		return items, page
	}
	page.NextCursor = encoded
	return items, page
}

func runStableListPosition(value RunView) (time.Time, string) {
	return value.CreatedAt, value.ID
}

func sessionStableListPosition(value SessionView) (time.Time, string) {
	return value.CreatedAt, value.ID
}

func threadStableListPosition(value ThreadView) (time.Time, string) {
	return value.CreatedAt, value.ID
}
