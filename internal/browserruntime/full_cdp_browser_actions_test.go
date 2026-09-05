package browserruntime

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestSummarizeFullCDPDocumentIsBoundedRedactedAndSelectorSafe(t *testing.T) {
	secret := "AKIAABCDEFGHIJKLMNOP"
	document, err := html.Parse(strings.NewReader(`<!doctype html><html><head>` +
		`<title>Local control page</title><style>.secret { display: none }</style></head>` +
		`<body><script>ignore this script</script><main><p>Visible ` + secret + ` text</p>` +
		`<input id="search" aria-label="Search reports"><button>Run report</button>` +
		`<section hidden><button id="hidden-button">Hidden action</button></section>` +
		`<div aria-hidden="true"><a id="hidden-link">Hidden link</a></div>` +
		`</main></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	title, text, elements, truncated := summarizeFullCDPDocument(document)
	if title != "Local control page" || truncated || len(elements) != 2 ||
		strings.Contains(text, secret) || !strings.Contains(text, "[REDACTED:aws-access-key]") ||
		strings.Contains(text, "ignore this script") ||
		elements[0].Selector != "#search" || elements[0].Name != "Search reports" ||
		!validFullCDPModelSelector(elements[1].Selector) ||
		strings.Contains(elements[1].Selector, "hidden") {
		t.Fatalf("title=%q text=%q elements=%#v truncated=%t", title, text, elements, truncated)
	}
	for _, element := range elements {
		if !validFullCDPModelSelector(element.Selector) {
			t.Fatalf("runtime emitted unsafe selector %q", element.Selector)
		}
	}
}

func TestSummarizeFullCDPDocumentCapsInteractiveElementsAndText(t *testing.T) {
	var source strings.Builder
	source.WriteString("<html><body>")
	for index := 0; index < MaxFullCDPPageSnapshotElements+8; index++ {
		source.WriteString("<button>bounded action</button>")
	}
	source.WriteString("<p>")
	source.WriteString(strings.Repeat("界", MaxFullCDPPageSnapshotTextBytes))
	source.WriteString("</p></body></html>")
	document, err := html.Parse(strings.NewReader(source.String()))
	if err != nil {
		t.Fatal(err)
	}
	_, text, elements, truncated := summarizeFullCDPDocument(document)
	if !truncated || len(elements) != MaxFullCDPPageSnapshotElements ||
		len([]byte(text)) > MaxFullCDPPageSnapshotTextBytes {
		t.Fatalf("text bytes=%d elements=%d truncated=%t",
			len([]byte(text)), len(elements), truncated)
	}
}

func TestFullCDPModelSelectorGrammarRejectsArbitraryCSS(t *testing.T) {
	for _, selector := range []string{"#search",
		"html:nth-of-type(1) > body:nth-of-type(1) > button:nth-of-type(2)",
		"body:nth-of-type(1) > input:nth-of-type(1)"} {
		if !validFullCDPModelSelector(selector) {
			t.Fatalf("rejected generated selector %q", selector)
		}
	}
	for _, selector := range []string{"button", "button:not([disabled])", "*",
		"div:nth-of-type(1)", "body:nth-of-type(0) > button:nth-of-type(1)",
		"body:nth-of-type(1)>button:nth-of-type(1)", "#bad selector"} {
		if validFullCDPModelSelector(selector) {
			t.Fatalf("accepted arbitrary selector %q", selector)
		}
	}
}
