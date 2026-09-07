//go:build desktop

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"cyberagent-workbench/internal/webui"
	webassets "cyberagent-workbench/web"
)

func TestEmbeddedProductionUIBundleLoadsApprovedFontsAndNotices(t *testing.T) {
	bundle, err := webui.LoadEmbeddedFS(webassets.Files, "dist")
	if err != nil {
		t.Fatalf("load embedded production UI: %v", err)
	}

	for _, current := range []struct {
		path   string
		marker string
	}{
		{path: "/THIRD-PARTY-NOTICES.txt", marker: "HarmonyOS Sans"},
		{path: "/licenses/HarmonyOS-Sans.txt", marker: "HarmonyOS Sans Fonts License Agreement"},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+current.path, nil)
		response := httptest.NewRecorder()
		bundle.ServeHTTP(response, request)
		if response.Code != http.StatusOK ||
			response.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
			response.Header().Get("Cache-Control") != "no-store" ||
			!strings.Contains(response.Body.String(), current.marker) {
			t.Fatalf("embedded notice %s: status=%d headers=%#v body=%q",
				current.path, response.Code, response.Header(), response.Body.String())
		}
	}

	approved := map[string]string{
		"c215d8ab1cb6709fec2e063f8213e9af86d7587d345b56325e36b67d6b947d98": "HarmonyOSSansSC-Bold",
		"7aa97804da2fc3802d116011b73ee25791303598718cc58dc49fedc9d63e5d2a": "HarmonyOSSansSC-Medium",
		"984cf609545acee8ef060780fb70fc3099b058c0553416331b6e863fdf7c26fa": "HarmonyOSSansSC-Regular",
		"794eaca447316607a98d46b9d3269271c285a19dfd26cad608e3e52368eb855e": "HarmonyOSSansSC-Semibold",
	}
	fontPaths, err := fs.Glob(webassets.Files, "dist/assets/HarmonyOSSansSC-*.ttf")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(fontPaths)
	if len(fontPaths) != len(approved) {
		t.Fatalf("embedded HarmonyOS Sans files = %v, want four approved TTFs", fontPaths)
	}
	for _, fontPath := range fontPaths {
		content, err := fs.ReadFile(webassets.Files, fontPath)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		got := hex.EncodeToString(digest[:])
		name, ok := approved[got]
		if !ok || !strings.Contains(fontPath, name) {
			t.Fatalf("embedded font %s has unapproved SHA-256 %s", fontPath, got)
		}
		delete(approved, got)
	}
	if len(approved) != 0 {
		t.Fatalf("embedded font set is missing approved digests: %#v", approved)
	}
}
