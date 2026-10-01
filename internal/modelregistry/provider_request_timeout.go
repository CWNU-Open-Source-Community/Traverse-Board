package modelregistry

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"cyberagent-workbench/internal/llm"
)

// Production configuration uses whole seconds. Internal HTTP clients may use
// shorter positive durations for bounded tests and adjacent callers.
func parseProviderRequestTimeout(value any) (time.Duration, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("Provider request_timeout_seconds must be an integer between 1 and 1800")
	}
	seconds, err := number.Int64()
	if err != nil || seconds < 1 || seconds > int64(llm.MaxProviderRequestTimeout/time.Second) {
		return 0, errors.New("Provider request_timeout_seconds must be an integer between 1 and 1800")
	}
	// Check the bound before multiplying, so neither parsing nor conversion can
	// turn an overflowing or negative value into an unbounded request.
	return time.Duration(seconds) * time.Second, nil
}

func environmentRequestTimeout(lookup EnvironmentLookup, name string) (time.Duration, error) {
	value, present := lookup(name)
	if !present {
		return llm.DefaultProviderRequestTimeout, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New("Provider request timeout environment value must be an integer between 1 and 1800")
	}
	return parseProviderRequestTimeout(json.Number(strconv.FormatInt(seconds, 10)))
}
