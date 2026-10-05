package llm

import (
	"errors"
	"net/http"
	"time"
)

const (
	// DefaultProviderRequestTimeout bounds the entire HTTP request, including
	// response-body reads. It is not a streaming idle timeout.
	DefaultProviderRequestTimeout = 60 * time.Second
	// MaxProviderRequestTimeout limits the resources one explicitly configured
	// long request can retain. Earlier caller and Run deadlines still apply.
	MaxProviderRequestTimeout = 30 * time.Minute
	defaultProviderTimeout    = DefaultProviderRequestTimeout
)

func providerHTTPClient(source *http.Client) (*http.Client, error) {
	client := &http.Client{}
	if source != nil {
		copy := *source
		client = &copy
	}
	if client.Timeout < 0 || client.Timeout > MaxProviderRequestTimeout {
		return nil, errors.New("provider request timeout must be positive and at most 30 minutes")
	}
	if client.Timeout == 0 {
		client.Timeout = defaultProviderTimeout
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client, nil
}

func applyOpenAIRequestHeaders(request *http.Request, stream bool, secret string,
	runtime HTTPProviderRuntime,
) error {
	request.Header.Set("Content-Type", "application/json")
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	} else {
		request.Header.Set("Accept", "application/json")
	}
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	return applyProviderRequestHeaders(runtime, secret, request.Header)
}
