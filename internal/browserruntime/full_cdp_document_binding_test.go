package browserruntime

import "testing"

func TestFullCDPDocumentBindingSurvivesFrontendNodeReallocation(t *testing.T) {
	runtime, server, _, _ := openBrowserActionFullCDPSession(t)
	setBrowserActionDocument(server, `<html><body><input id="search" type="text"></body></html>`)
	server.mu.Lock()
	server.rotateDOMNodeIDs = true
	server.mu.Unlock()
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatalf("stable backend document rejected after frontend node IDs rotated: %v", err)
	}
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "verified input"); err != nil {
		t.Fatalf("type retained an invalidated frontend node reference: %v", err)
	}
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ClickFullCDP(t.Context(), "#search"); err != nil {
		t.Fatalf("click retained an invalidated frontend node reference: %v", err)
	}
	if _, err := runtime.ScreenshotFullCDP(t.Context()); err != nil {
		t.Fatalf("screenshot rejected stable backend document: %v", err)
	}
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.rootBackendNodeID++
	server.mu.Unlock()
	before := server.MethodCount()
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "must not execute"); err == nil {
		t.Fatal("same HTML with replacement backend root reused prior selector authority")
	}
	if containsString(server.Methods()[before:], "Input.insertText") {
		t.Fatal("replacement document reached input dispatch")
	}
	server.mu.Lock()
	server.rootBackendNodeID = 0
	server.mu.Unlock()
	if _, err := runtime.SnapshotFullCDP(t.Context()); err == nil {
		t.Fatal("snapshot accepted missing stable backend document identity")
	}
}

func TestFullCDPTypeAllowsStateTextUpdateButRejectsReplacedInput(t *testing.T) {
	for _, replaceInput := range []bool{false, true} {
		name := "state_text_update"
		if replaceInput {
			name = "input_replaced"
		}
		t.Run(name, func(t *testing.T) {
			runtime, server, _, _ := openBrowserActionFullCDPSession(t)
			setBrowserActionDocument(server, `<html><body><input id="search" type="text"><p>Waiting</p></body></html>`)
			server.mu.Lock()
			server.rotateDOMNodeIDs = true
			server.mutateHTMLOnInput = `<html><body><input id="search" type="text" value="new text"><p>New text received</p></body></html>`
			server.mutateBackendOnInput = replaceInput
			server.mu.Unlock()
			if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
				t.Fatal(err)
			}
			before := server.MethodCount()
			_, err := runtime.TypeFullCDP(t.Context(), "#search", "new text")
			if !containsString(server.Methods()[before:], "Input.insertText") {
				t.Fatal("test did not reach the real CDP input boundary")
			}
			if (err != nil) != replaceInput {
				t.Fatalf("replaceInput=%t err=%v", replaceInput, err)
			}
			if _, err := runtime.TypeFullCDP(t.Context(), "#search", "must not repeat"); err == nil {
				t.Fatal("mutated action left reusable selector authority")
			}
		})
	}
}

func TestFullCDPSnapshotKeepsDisabledAndHiddenControlsWithoutActionAuthority(t *testing.T) {
	runtime, server, _, _ := openBrowserActionFullCDPSession(t)
	setBrowserActionDocument(server, `<html><body><input id="disabled" type="text" disabled><input id="hidden" type="text" hidden><div aria-hidden="true"><input id="hidden-child" type="text"></div><input id="search" type="text"></body></html>`)
	server.mu.Lock()
	server.elementAttrsBySelector = map[string][]string{
		"#disabled": {"id", "disabled", "type", "text", "disabled", ""},
		"#search":   {"id", "search", "type", "text"},
	}
	server.mu.Unlock()
	snapshot, err := runtime.SnapshotFullCDP(t.Context())
	if err != nil {
		t.Fatalf("inactive controls blocked the readable page snapshot: %v", err)
	}
	if len(snapshot.Elements) != 2 || snapshot.Elements[0].Selector != "#disabled" || !snapshot.Elements[0].Disabled ||
		snapshot.Elements[1].Selector != "#search" || snapshot.Elements[1].Disabled {
		t.Fatalf("incorrect readable controls: %+v", snapshot.Elements)
	}
	if len(runtime.selectorSnapshot.Selectors) != 1 {
		t.Fatalf("inactive controls received action provenance: %+v", runtime.selectorSnapshot.Selectors)
	}
	for _, selector := range []string{"#disabled", "#hidden", "#hidden-child"} {
		if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
			t.Fatal(err)
		}
		before := server.MethodCount()
		if _, err := runtime.ClickFullCDP(t.Context(), selector); err == nil {
			t.Fatalf("inactive selector %s accepted click", selector)
		}
		if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.TypeFullCDP(t.Context(), selector, "must not write"); err == nil {
			t.Fatalf("inactive selector %s accepted input", selector)
		}
		methods := server.Methods()[before:]
		if containsString(methods, "Input.insertText") || containsString(methods, "Input.dispatchMouseEvent") {
			t.Fatalf("inactive selector %s reached action dispatch", selector)
		}
	}
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "editable"); err != nil {
		t.Fatalf("normal input was blocked by unrelated inactive controls: %v", err)
	}
	for _, attribute := range []string{"disabled", "hidden"} {
		server.mu.Lock()
		server.elementAttrsBySelector["#search"] = []string{"id", "search", "type", "text", attribute, ""}
		server.mu.Unlock()
		if _, err := runtime.SnapshotFullCDP(t.Context()); err == nil {
			t.Fatalf("new %s state absent from the HTML observation was silently swallowed", attribute)
		}
	}
}
