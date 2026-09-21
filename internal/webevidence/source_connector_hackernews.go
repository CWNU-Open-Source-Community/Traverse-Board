package webevidence

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const hackerNewsConnectorVersion = "hacker-news-public.v1"

type HackerNewsSourceConnector struct{ client *SafeHTTPClient }

func NewHackerNewsSourceConnector(client *SafeHTTPClient) *HackerNewsSourceConnector {
	if client == nil {
		client = NewSafeHTTPClient()
	}
	return &HackerNewsSourceConnector{client: client}
}

func (*HackerNewsSourceConnector) Name() string    { return "hacker_news" }
func (*HackerNewsSourceConnector) Version() string { return hackerNewsConnectorVersion }
func (*HackerNewsSourceConnector) SearchEndpoint() string {
	return "https://hn.algolia.com/api/v1/search"
}

func (*HackerNewsSourceConnector) MatchURL(rawURL string) bool {
	_, ok := parseHackerNewsURL(rawURL)
	return ok
}

func (c *HackerNewsSourceConnector) Search(ctx context.Context, query string, limit int,
	authority NetworkAuthority,
) ([]ConnectorSearchItem, error) {
	endpoint, _ := url.Parse(c.SearchEndpoint())
	parameters := endpoint.Query()
	parameters.Set("query", query)
	parameters.Set("hitsPerPage", strconv.Itoa(limit))
	parameters.Set("tags", "story")
	endpoint.RawQuery = parameters.Encode()
	var response struct {
		Hits []struct {
			ObjectID  string `json:"objectID"`
			Title     string `json:"title"`
			StoryText string `json:"story_text"`
			URL       string `json:"url"`
			Author    string `json:"author"`
			CreatedAt string `json:"created_at"`
			Points    int    `json:"points"`
			Comments  int    `json:"num_comments"`
		} `json:"hits"`
	}
	_, err := connectorGetJSON(ctx, c.client, endpoint.String(), authority, &response)
	if err != nil {
		return nil, err
	}
	items := make([]ConnectorSearchItem, 0, min(limit, len(response.Hits)))
	for _, hit := range response.Hits {
		id, parseErr := strconv.ParseInt(hit.ObjectID, 10, 64)
		if parseErr != nil || id < 1 {
			continue
		}
		permalink := fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id)
		canonical, _ := CanonicalizePublicHTTPSURL(permalink)
		snippet := fmt.Sprintf("@%s · %d points · %d comments", hit.Author,
			hit.Points, hit.Comments)
		if text := connectorHTMLText(hit.StoryText); text != "" {
			snippet += " · " + text
		} else if strings.TrimSpace(hit.URL) != "" {
			snippet += " · " + hit.URL
		}
		items = append(items, ConnectorSearchItem{URL: canonical,
			Title: boundedCleanText(hit.Title, 1024), Snippet: boundedSnippet(snippet),
			PublishedAt: boundedCleanText(hit.CreatedAt, 128), ExternalID: hit.ObjectID})
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

type hackerNewsItem struct {
	ID          int64   `json:"id"`
	By          string  `json:"by"`
	Time        int64   `json:"time"`
	Text        string  `json:"text"`
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Type        string  `json:"type"`
	Kids        []int64 `json:"kids"`
	Descendants int     `json:"descendants"`
	Deleted     bool    `json:"deleted"`
	Dead        bool    `json:"dead"`
}

func (c *HackerNewsSourceConnector) Read(ctx context.Context, rawURL string, maxItems int,
	authority NetworkAuthority,
) (ConnectorDocument, error) {
	id, ok := parseHackerNewsURL(rawURL)
	if !ok {
		return ConnectorDocument{}, newConnectorError("unsupported_url", nil)
	}
	if maxItems <= 0 {
		maxItems = DefaultConnectorItemLimit
	}
	if maxItems > MaxConnectorItemLimit {
		return ConnectorDocument{}, newConnectorError("invalid_request", nil)
	}
	canonical, _ := CanonicalizePublicHTTPSURL(
		fmt.Sprintf("https://news.ycombinator.com/item?id=%d", id))
	fetchItem := func(itemID int64) (hackerNewsItem, HTTPDocument, error) {
		endpoint := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", itemID)
		var item hackerNewsItem
		document, err := connectorGetJSON(ctx, c.client, endpoint, authority, &item)
		if err != nil {
			return hackerNewsItem{}, HTTPDocument{}, err
		}
		if item.ID != itemID {
			return hackerNewsItem{}, HTTPDocument{}, newConnectorHTTPError("invalid_response", document, nil)
		}
		return item, document, nil
	}
	root, rootDocument, err := fetchItem(id)
	if err != nil {
		return ConnectorDocument{}, err
	}
	type queuedItem struct {
		id     int64
		parent int64
		depth  int
	}
	queue := make([]queuedItem, 0, len(root.Kids))
	for _, child := range root.Kids {
		queue = append(queue, queuedItem{id: child, parent: root.ID, depth: 1})
	}
	requestDocuments := []HTTPDocument{rootDocument}
	requestEndpoints := []string{rootDocument.RequestedURL}
	type commentRecord struct {
		item   hackerNewsItem
		parent int64
		depth  int
	}
	comments := make([]commentRecord, 0, max(0, min(maxItems, root.Descendants)))
	var continuationFailure *ConnectorFailure
	depthLimitReached := false
	for len(queue) > 0 && len(comments) < maxItems {
		current := queue[0]
		queue = queue[1:]
		item, document, fetchErr := fetchItem(current.id)
		if fetchErr != nil {
			continuationFailure = connectorFailure(c.Name(), fetchErr)
			requestEndpoints = append(requestEndpoints,
				fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", current.id))
			break
		}
		requestDocuments = append(requestDocuments, document)
		requestEndpoints = append(requestEndpoints, document.RequestedURL)
		comments = append(comments, commentRecord{item: item, parent: current.parent,
			depth: current.depth})
		if current.depth < 4 {
			for _, child := range item.Kids {
				queue = append(queue, queuedItem{id: child, parent: item.ID,
					depth: current.depth + 1})
			}
		} else if len(item.Kids) > 0 {
			depthLimitReached = true
		}
	}
	var builder strings.Builder
	rootTitle := boundedConnectorText(root.Title, 4096)
	if rootTitle == "" {
		rootTitle = fmt.Sprintf("Hacker News item %d", root.ID)
	}
	metadata := fmt.Sprintf("URL: %s\nAuthor: %s\nPublished: %s",
		canonical, boundedConnectorText(root.By, 256),
		time.Unix(root.Time, 0).UTC().Format(time.RFC3339))
	if strings.TrimSpace(root.URL) != "" {
		metadata += "\nArticle: " + boundedConnectorText(root.URL, 4096)
	}
	markdownSection(&builder, "# "+rootTitle, metadata)
	markdownSection(&builder, "## Post", connectorHTMLText(root.Text))
	if len(comments) > 0 {
		var commentsBody strings.Builder
		for index, record := range comments {
			if index > 0 {
				commentsBody.WriteString("\n\n")
			}
			status := ""
			if record.item.Deleted {
				status = " [deleted]"
			} else if record.item.Dead {
				status = " [dead]"
			}
			fmt.Fprintf(&commentsBody,
				"### Comment %d (parent %d, depth %d) by %s%s at %s\n\n%s",
				record.item.ID, record.parent, record.depth,
				boundedConnectorText(record.item.By, 256), status,
				time.Unix(record.item.Time, 0).UTC().Format(time.RFC3339),
				connectorHTMLText(record.item.Text))
		}
		heading := fmt.Sprintf("## Comments (%d included; total unknown)", len(comments))
		if root.Type != "comment" {
			heading = fmt.Sprintf("## Comments (%d of %d)", len(comments), root.Descendants)
		}
		markdownSection(&builder, heading, commentsBody.String())
	}
	truncated := root.Descendants > len(comments) || len(queue) > 0 || depthLimitReached || continuationFailure != nil
	truncationReason := ""
	if truncated {
		truncationReason = "comment_or_depth_limit"
	}
	if continuationFailure != nil {
		truncationReason = "comment_read_failed"
	}
	itemsAvailable := max(root.Descendants, len(comments)) + 1
	if root.Type == "comment" {
		itemsAvailable = 0
	} // The API does not report a subtree total.
	return ConnectorDocument{CanonicalURL: canonical, RequestEndpoints: requestEndpoints,
		HTTPStatus: rootDocument.StatusCode,
		RawDigest:  combineConnectorRawDigest(requestDocuments...),
		Title:      boundedCleanText(rootTitle, 1024), Byline: boundedCleanText(root.By, 512),
		PublishedAt: time.Unix(root.Time, 0).UTC().Format(time.RFC3339),
		MIME:        "text/markdown", Charset: "utf-8", Body: builder.String(),
		ContentKind: "hacker_news_thread", Coverage: "post_and_comment_tree_depth_4",
		ItemsIncluded: len(comments) + 1, ItemsAvailable: itemsAvailable,
		Truncated: truncated, TruncationReason: truncationReason,
		ContinuationFailure: continuationFailure}, nil
}

func parseHackerNewsURL(rawURL string) (int64, bool) {
	canonical, err := CanonicalizePublicHTTPSURL(rawURL)
	if err != nil {
		return 0, false
	}
	parsed, _ := url.Parse(canonical)
	if parsed.Hostname() != "news.ycombinator.com" || parsed.Path != "/item" {
		return 0, false
	}
	id, err := strconv.ParseInt(parsed.Query().Get("id"), 10, 64)
	return id, err == nil && id > 0
}
