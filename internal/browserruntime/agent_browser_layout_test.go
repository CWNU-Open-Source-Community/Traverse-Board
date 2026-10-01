package browserruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentBrowserViewportRejectsBeforeRuntimeDispatch(t *testing.T) {
	r := &AgentBrowserRuntime{}
	for _, v := range []AgentBrowserViewport{{239, 844}, {390, 2161}, {3841, 844}, {390, 0}} {
		if _, err := r.NavigateWithViewport(t.Context(), "https://example.org", v); err == nil {
			t.Fatalf("invalid viewport reached inactive runtime: %+v", v)
		}
	}
}

func TestAgentBrowserSnapshotByteBoundKeepsJSONAndRefPairing(t *testing.T) {
	s := AgentBrowserSnapshot{Text: strings.Repeat("<", 16384), Layout: &AgentBrowserLayout{}}
	refs := map[string]agentBrowserRef{}
	for i := 0; i < 128; i++ {
		ref := fmt.Sprint(i)
		s.Elements = append(s.Elements, AgentBrowserElement{Ref: ref, Name: strings.Repeat("<", 512)})
		refs[ref] = agentBrowserRef{}
	}
	for i := 0; i < 48; i++ {
		s.Layout.Nodes = append(s.Layout.Nodes, AgentBrowserLayoutNode{Name: strings.Repeat("<", 160), RenderedText: strings.Repeat("<", 160), Focused: true})
	}
	if err := boundAgentBrowserSnapshot(&s, refs); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(s)
	if len(encoded) > 120*1024 || !json.Valid(encoded) || !s.Truncated || !s.Layout.Truncated || len(refs) != len(s.Elements) {
		t.Fatalf("invalid bounded observation: bytes=%d elements=%d refs=%d", len(encoded), len(s.Elements), len(refs))
	}
	for _, e := range s.Elements {
		if _, ok := refs[e.Ref]; !ok {
			t.Fatal("advertised ref lost")
		}
	}
}

func TestAgentBrowserRealViewportAndLayoutEvidence(t *testing.T) {
	r, _ := agentBrowserTestRuntime(t)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if req.URL.Path == "/bounded" {
			fmt.Fprint(w, `<main>`+strings.Repeat(`<section title="bounded">x</section>`, 60)+`</main>`)
			return
		}
		if req.URL.Path == "/scan" {
			fmt.Fprint(w, strings.Repeat(`<div>x</div>`, 300)+`<section id="past-bound">not observed</section>`)
			return
		}
		fmt.Fprint(w, `<meta name="viewport" content="width=device-width, initial-scale=1"><style>body{margin:0}main{display:flex;flex-direction:column}form{order:2;height:100px}#queue{order:1;height:100px}#long{display:block;width:100px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}@media(max-width:400px){#queue{width:500px}}</style><main><form id="composer"><button>add</button></form><section id="queue" aria-label="pending"><span id="long" title="Full long message">This long message is definitely clipped at 100 CSS pixels.</span></section></main>`)
	}))
	defer site.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	var previous AgentBrowserSnapshot
	for _, v := range []AgentBrowserViewport{{1280, 900}, {390, 844}} {
		nav, err := r.NavigateWithViewport(ctx, site.URL, v)
		if err != nil || nav.Viewport == nil || *nav.Viewport != v {
			t.Fatalf("actual viewport: %+v %v", nav, err)
		}
		if len(previous.Elements) > 0 {
			if _, err := r.Click(ctx, previous.SnapshotID, previous.Elements[0].Ref); !errors.Is(err, ErrAgentBrowserStaleReference) {
				t.Fatalf("resize kept old refs: %v", err)
			}
		}
		snapshot, err := r.Snapshot(ctx)
		if err != nil || snapshot.Layout == nil || snapshot.Layout.Viewport != v || snapshot.Layout.Truncated {
			t.Fatalf("layout snapshot: %+v %v", snapshot, err)
		}
		nodes := map[string]AgentBrowserLayoutNode{}
		for _, n := range snapshot.Layout.Nodes {
			nodes[n.ID] = n
		}
		queue, composer, long := nodes["queue"], nodes["composer"], nodes["long"]
		if queue.Rect.Height == 0 || queue.Rect.Y+queue.Rect.Height > composer.Rect.Y || long.ScrollWidth <= long.ClientWidth || long.WhiteSpace != "nowrap" || long.TextOverflow != "ellipsis" || long.TabIndex != -1 {
			t.Fatalf("spatial/clipping evidence missing: %+v", nodes)
		}
		if (snapshot.Layout.ScrollWidth > snapshot.Layout.ClientWidth) != (v.Width == 390) {
			t.Fatalf("overflow observation: %+v", snapshot.Layout)
		}
		encoded, _ := json.Marshal(snapshot)
		if len(encoded) >= 128*1024 {
			t.Fatal("snapshot exceeds gateway receipt bound")
		}
		t.Logf("actual viewport=%+v queueBottom=%.2f composerTop=%.2f doc=%d/%d long=%d/%d bytes=%d", *nav.Viewport, queue.Rect.Y+queue.Rect.Height, composer.Rect.Y, snapshot.Layout.ScrollWidth, snapshot.Layout.ClientWidth, long.ScrollWidth, long.ClientWidth, len(encoded))
		previous = snapshot
	}
	for _, path := range []string{"/bounded", "/scan"} {
		if _, err := r.NavigateWithViewport(ctx, site.URL+path, AgentBrowserViewport{1280, 2160}); err != nil {
			t.Fatal(err)
		}
		snapshot, err := r.Snapshot(ctx)
		if err != nil || snapshot.Layout == nil || !snapshot.Layout.Truncated || len(snapshot.Layout.Nodes) > 48 || snapshot.Layout.Scanned > 256 {
			t.Fatalf("layout scan/result bound: %+v %v", snapshot.Layout, err)
		}
		for _, n := range snapshot.Layout.Nodes {
			if n.ID == "past-bound" {
				t.Fatal("traversed past declared scan bound")
			}
		}
		t.Logf("bounded layout path=%s scanned=%d returned=%d truncated=%v", path, snapshot.Layout.Scanned, len(snapshot.Layout.Nodes), snapshot.Layout.Truncated)
	}
}

func TestAgentBrowserRealViewportCancellationClosesOwnedTarget(t *testing.T) {
	r, _ := agentBrowserTestRuntime(t)
	entered := make(chan struct{}, 1)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-req.Context().Done()
	}))
	defer site.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := r.NavigateWithViewport(ctx, site.URL, AgentBrowserViewport{390, 844})
		result <- err
	}()
	select {
	case <-entered:
		cancel()
	case <-ctx.Done():
		t.Fatal("navigation did not reach fixture")
	}
	select {
	case err := <-result:
		var action *AgentBrowserActionError
		if !errors.As(err, &action) || !action.OutcomeUnknown {
			t.Fatalf("cancel after dispatch lost unknown receipt: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not settle")
	}
	if _, err := r.Snapshot(t.Context()); !errors.Is(err, ErrAgentBrowserClosed) {
		t.Fatalf("cancelled resized target remained reusable: %v", err)
	}
}

func TestAgentBrowserRealLayoutExcludesInvisibleAndLabelsTitle(t *testing.T) {
	r, _ := agentBrowserTestRuntime(t)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<main><section id="transparent" style="opacity:0">invisible</section><div style="opacity:0"><section id="ancestor-opacity">invisible</section></div><div style="height:10px;overflow:hidden"><section id="ancestor-clip" style="margin-top:50px">invisible</section></div><span id="title-only" title="Full title" style="display:block;width:50px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">Visible clipped message</span><section id="masked" style="clip-path:circle(40%)">uncertain</section><div style="visibility:hidden"><section id="visibility-override" style="visibility:visible">visible override</section></div><div style="height:1px;overflow:hidden"><section id="escaped" style="position:fixed;top:300px">fixed popup</section></div></main>`)
	}))
	defer site.Close()
	if _, err := r.NavigateWithViewport(t.Context(), site.URL, AgentBrowserViewport{390, 844}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.Snapshot(t.Context())
	if err != nil || snapshot.Layout == nil {
		t.Fatalf("layout: %v", err)
	}
	seen := map[string]AgentBrowserLayoutNode{}
	for _, n := range snapshot.Layout.Nodes {
		seen[n.ID] = n
	}
	for _, id := range []string{"transparent", "ancestor-opacity", "ancestor-clip"} {
		if _, ok := seen[id]; ok {
			t.Fatalf("invisible node presented as viewport evidence: %s", id)
		}
	}
	if seen["title-only"].NameSource != "title" || seen["title-only"].Name != "Full title" || seen["title-only"].ScrollWidth <= seen["title-only"].ClientWidth || !seen["masked"].VisibilityUncertain {
		t.Fatalf("title/complex clipping misrepresented: %+v", seen)
	}
	if seen["visibility-override"].Rect.Height == 0 || !seen["escaped"].VisibilityUncertain || seen["escaped"].Rect.Height == 0 {
		t.Fatalf("visible override/escaped popup erased: %+v", seen)
	}
	t.Logf("hidden nodes filtered; title label=%s clip uncertainty=%v", seen["title-only"].NameSource, seen["masked"].VisibilityUncertain)
}

func TestAgentBrowserRealLayoutObservesLeafTextAndFocus(t *testing.T) {
	r, _ := agentBrowserTestRuntime(t)
	wide := strings.Repeat("宽", 24)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<style>#long{display:block;width:100px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}</style><p id="paragraph">Actual paragraph</p><span id="long" aria-label="Label differs from body">`+wide+`</span><span id="bounded">`+strings.Repeat("😀", 170)+`</span><p id="editable" contenteditable>private draft</p><span><span contenteditable>nested draft</span></span><button id="toggle" onclick="document.querySelector('#long').style.whiteSpace='normal';this.focus()">全文</button><button id="other">其他</button><textarea id="draft">textarea draft</textarea>`)
	}))
	defer site.Close()
	if _, err := r.NavigateWithViewport(t.Context(), site.URL, AgentBrowserViewport{390, 844}); err != nil {
		t.Fatal(err)
	}
	inspect := func() (AgentBrowserSnapshot, map[string]map[string]any) {
		s, err := r.Snapshot(t.Context())
		if err != nil || s.Layout == nil || s.Layout.Truncated {
			t.Fatalf("snapshot: %v", err)
		}
		encoded, _ := json.Marshal(s.Layout.Nodes)
		var nodes []map[string]any
		_ = json.Unmarshal(encoded, &nodes)
		byID := map[string]map[string]any{}
		for _, n := range nodes {
			byID[fmt.Sprint(n["id"])] = n
		}
		return s, byID
	}
	snapshot, nodes := inspect()
	if nodes["long"]["rendered_text"] != wide || nodes["long"]["name"] != "Label differs from body" ||
		nodes["long"]["white_space"] != "nowrap" || nodes["long"]["scroll_width"].(float64) <= nodes["long"]["client_width"].(float64) ||
		nodes["paragraph"]["rendered_text"] != "Actual paragraph" {
		t.Fatalf("real body text/clipping is absent or confused with label: %+v", nodes["long"])
	}
	if nodes["bounded"]["rendered_text"] != strings.Repeat("😀", 160) || nodes["bounded"]["text_truncated"] != true {
		t.Fatal("bounded body text splits Unicode or hides excerpt truncation", nodes["bounded"])
	}
	for _, id := range []string{"editable", "draft"} {
		if v := nodes[id]["rendered_text"]; v != nil && v != "" {
			t.Fatalf("input value appeared in new body observation: %s %+v", id, nodes[id])
		}
	}
	click := func(s AgentBrowserSnapshot, name string) {
		for _, e := range s.Elements {
			if e.Name == name {
				if _, err := r.Click(t.Context(), s.SnapshotID, e.Ref); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatal("missing control", name)
	}
	click(snapshot, "全文")
	snapshot, nodes = inspect()
	if nodes["toggle"]["focused"] != true || nodes["long"]["white_space"] != "normal" || nodes["long"]["scroll_width"] != nodes["long"]["client_width"] {
		t.Fatal("full body/focus observation missing", nodes["long"], nodes["toggle"])
	}
	click(snapshot, "其他")
	_, nodes = inspect()
	if nodes["toggle"]["focused"] == true || nodes["other"]["focused"] != true {
		t.Fatal("focus observation stuck on previous control", nodes["toggle"], nodes["other"])
	}
}
