package webevidence

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const DefaultWebSearchEndpoint = "https://html.duckduckgo.com/html/"

// WebSearchProvider reads DuckDuckGo's ordinary HTML search results. It does not ask the chat
// model to manufacture a results list and needs no model API credentials.
// Results remain discovery hints: the normal fetch/citation path verifies
// pages before they can be cited. A changed result page or challenge is an error,
// never an invitation to scrape around an access restriction.
type WebSearchProvider struct{ client *SafeHTTPClient }

func NewWebSearchProvider(client *SafeHTTPClient) *WebSearchProvider {
	if client == nil {
		client = NewSafeHTTPClient()
	}
	bounded := *client
	bounded.Timeout = DefaultRequestTimeout
	return &WebSearchProvider{client: &bounded}
}

func (*WebSearchProvider) Name() string     { return "duckduckgo" }
func (*WebSearchProvider) Endpoint() string { return DefaultWebSearchEndpoint }

func (p *WebSearchProvider) Search(ctx context.Context, query string,
	limit int, authority NetworkAuthority,
) ([]ProviderResult, error) {
	if p == nil || p.client == nil || ctx == nil {
		return nil, errors.New("web search provider is unavailable")
	}
	if query == "" || query != boundedCleanText(query, MaxQueryRunes) || limit < 1 || limit > MaxSources {
		return nil, errors.New("web search query or result limit is invalid")
	}
	// Use the normal first-page form also used by SearXNG's duckduckgo engine.
	// No pagination tokens, scripts, challenge handling, or forwarded credentials.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	parameters := url.Values{"q": {query}, "b": {""}, "kl": {"wt-wt"}}
	document, err := p.client.PostFormAuthorizedNoRedirect(ctx, DefaultWebSearchEndpoint,
		[]byte(parameters.Encode()), DefaultMaxResponse, func(raw string) error {
			_, err := authority.Authorize(raw)
			return err
		})
	if err != nil {
		return nil, err
	}
	if document.StatusCode != http.StatusOK || document.Truncated {
		return nil, errors.New("web search page was unavailable or exceeded the response limit")
	}
	mediaType, _, err := mime.ParseMediaType(document.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return nil, errors.New("web search returned an unsupported page")
	}
	return parseWebSearchPage(document.Body, limit)
}

// Result locations follow the normal DuckDuckGo HTML structure also used by
// SearXNG (searx/engines/duckduckgo.py): web-result entries under links, a
// heading link and result__snippet. We reuse the HTML dependency without executing
// scripts, keeping ads, navigation links and generated answers out of results.
func parseWebSearchPage(body []byte, limit int) ([]ProviderResult, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, errors.New("web search returned an invalid page")
	}
	challenge := findSearchNode(document, func(n *html.Node) bool {
		classes := " " + strings.Join(strings.Fields(searchAttribute(n, "class")), " ") + " "
		return (n.Data == "form" && searchAttribute(n, "id") == "challenge-form") ||
			strings.Contains(classes, " g-recaptcha ") || strings.Contains(classes, " h-captcha ")
	})
	if challenge != nil {
		return nil, errors.New("web search requires an access challenge")
	}
	container := findSearchNode(document, func(n *html.Node) bool {
		return n.Data == "div" && searchAttribute(n, "id") == "links"
	})
	if container == nil {
		return nil, errors.New("web search results were missing or an access challenge was returned")
	}
	results := make([]ProviderResult, 0, limit)
	seen := make(map[string]bool)
	empty := false
	for item := container.FirstChild; item != nil; item = item.NextSibling {
		if item.Type != html.ElementNode || item.Data != "div" {
			continue
		}
		classes := " " + strings.Join(strings.Fields(searchAttribute(item, "class")), " ") + " "
		empty = empty || strings.Contains(classes, " no-results ") || strings.Contains(classes, " result--no-result ")
		if !strings.Contains(classes, " web-result ") || strings.Contains(classes, " result--ad ") {
			continue
		}
		heading := findSearchNode(item, func(n *html.Node) bool { return n.Data == "h2" })
		link := findSearchNode(heading, func(n *html.Node) bool { return n.Data == "a" })
		if link == nil {
			continue
		}
		canonical, err := CanonicalizePublicHTTPSURL(searchAttribute(link, "href"))
		if err != nil || seen[canonical] {
			continue
		}
		title := boundedCleanText(searchNodeText(link), 1024)
		if title == "" {
			continue
		}
		seen[canonical] = true
		paragraph := findSearchNode(item, func(n *html.Node) bool {
			return n.Data == "a" && strings.Contains(" "+searchAttribute(n, "class")+" ", " result__snippet ")
		})
		results = append(results, ProviderResult{URL: canonical, Title: title,
			Snippet: boundedSnippet(searchNodeText(paragraph)), Rank: len(results) + 1})
		if len(results) == limit {
			break
		}
	}
	if len(results) == 0 && !empty {
		return nil, errors.New("web search page contains no usable public HTTPS results")
	}
	return results, nil
}

func searchAttribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func findSearchNode(node *html.Node, match func(*html.Node) bool) *html.Node {
	if node == nil {
		return nil
	}
	if node.Type == html.ElementNode && match(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findSearchNode(child, match); found != nil {
			return found
		}
	}
	return nil
}

func searchNodeText(node *html.Node) string {
	if node == nil || (node.Type == html.ElementNode &&
		(node.Data == "script" || node.Data == "style" || node.Data == "template")) {
		return ""
	}
	if node.Type == html.TextNode {
		return node.Data
	}
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(searchNodeText(child))
		text.WriteByte(' ')
	}
	return text.String()
}
