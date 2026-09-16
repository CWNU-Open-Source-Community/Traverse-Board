package contextmgr

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Complete source text does not need the same digest twice. Original source
// identity and authority remain explicit; excerpts always have their own hash.
// Old records containing both hashes remain readable without rewriting them.
func (record handoffMemoryRecord) MarshalJSON() ([]byte, error) {
	type wireRecord handoffMemoryRecord
	value := wireRecord(record)
	if validHandoffDigest(value.SourceContentSHA256) && value.ContentSHA256 == value.SourceContentSHA256 &&
		handoffContentSHA256(value.Content) == value.SourceContentSHA256 {
		value.ContentSHA256 = ""
	}
	return json.Marshal(value)
}

func (record *handoffMemoryRecord) UnmarshalJSON(data []byte) error {
	type wireRecord handoffMemoryRecord
	var value wireRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("handoff record has trailing data")
	}
	if value.ContentSHA256 == "" {
		if !validHandoffDigest(value.SourceContentSHA256) || handoffContentSHA256(value.Content) != value.SourceContentSHA256 {
			return errors.New("complete handoff record does not match its original source hash")
		}
		value.ContentSHA256 = value.SourceContentSHA256
	}
	*record = handoffMemoryRecord(value)
	return nil
}
