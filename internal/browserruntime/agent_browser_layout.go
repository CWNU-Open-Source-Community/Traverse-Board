package browserruntime

import (
	"encoding/json"
	"errors"
)

// CSS pixels in the owned target. This does not emulate touch or a phone device.
type AgentBrowserViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (v AgentBrowserViewport) validate() error {
	if v.Width < 240 || v.Width > 3840 || v.Height < 240 || v.Height > 2160 {
		return errors.New("browser viewport outside supported CSS dimensions")
	}
	return nil
}

type AgentBrowserRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type AgentBrowserLayoutNode struct {
	Tag                 string           `json:"tag"`
	ID                  string           `json:"id,omitempty"`
	Name                string           `json:"name"`
	NameSource          string           `json:"name_source"`
	RenderedText        string           `json:"rendered_text,omitempty"`
	TextTruncated       bool             `json:"text_truncated,omitempty"`
	Focused             bool             `json:"focused,omitempty"`
	Rect                AgentBrowserRect `json:"rect"`
	VisibleRect         AgentBrowserRect `json:"visible_rect"`
	VisibilityUncertain bool             `json:"visibility_uncertain"`
	ClientWidth         int              `json:"client_width"`
	ScrollWidth         int              `json:"scroll_width"`
	WhiteSpace          string           `json:"white_space"`
	OverflowX           string           `json:"overflow_x"`
	TextOverflow        string           `json:"text_overflow"`
	TabIndex            int              `json:"tab_index"`
}

type AgentBrowserLayout struct {
	Viewport     AgentBrowserViewport     `json:"viewport"`
	ClientWidth  int                      `json:"client_width"`
	ScrollWidth  int                      `json:"scroll_width"`
	ClientHeight int                      `json:"client_height"`
	ScrollHeight int                      `json:"scroll_height"`
	Nodes        []AgentBrowserLayoutNode `json:"nodes"`
	Scanned      int                      `json:"scanned"`
	Truncated    bool                     `json:"truncated"`
	Coverage     string                   `json:"coverage"`
}

// Leave room below the gateway's existing 128 KiB stdout bound. Removing whole
// observations preserves valid JSON and exact pairing of advertised refs.
func boundAgentBrowserSnapshot(snapshot *AgentBrowserSnapshot, refs map[string]agentBrowserRef) error {
	for {
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if len(encoded) <= 120*1024 {
			return nil
		}
		snapshot.Truncated = true
		switch {
		case snapshot.Text != "":
			runes := []rune(snapshot.Text)
			snapshot.Text = string(runes[:max(0, len(runes)-1024)])
		case snapshot.Layout != nil && len(snapshot.Layout.Nodes) > 0:
			snapshot.Layout.Nodes = snapshot.Layout.Nodes[:len(snapshot.Layout.Nodes)-1]
			snapshot.Layout.Truncated = true
		case len(snapshot.Elements) > 0:
			last := snapshot.Elements[len(snapshot.Elements)-1]
			delete(refs, last.Ref)
			snapshot.Elements = snapshot.Elements[:len(snapshot.Elements)-1]
		default:
			return errors.New("browser snapshot identity exceeds result bound")
		}
	}
}

// Fixed, read-only observation in the isolated world; no model script, selector,
// input values or subframe traversal. Bound both DOM scanning and result size.
const agentBrowserDocumentFunction = `function(){
const s=(this.body&&this.body.innerText)||'',root=this.documentElement;
const view=this.defaultView, nodes=[], walk=this.createTreeWalker(this.body||root,1);
const clippedRect=el=>{
 const r=el.getBoundingClientRect();
 const own=view.getComputedStyle(el);
 if(own.visibility==='hidden'||own.visibility==='collapse')return null;
 let left=Math.max(0,r.left),top=Math.max(0,r.top),right=Math.min(view.innerWidth,r.right),bottom=Math.min(view.innerHeight,r.bottom),p=el,depth=0,uncertain=false,escaped=false;
 while(p&&depth<32){
  const c=view.getComputedStyle(p);
  if(c.display==='none'||Number(c.opacity)===0)return null;
  if(c.clipPath!=='none'||c.maskImage!=='none'||c.clip!=='auto'||c.filter!=='none')uncertain=true;
  if(c.position==='fixed'||c.position==='absolute'){escaped=true;uncertain=true;}
  try{if(p.matches(':modal,:popover-open')){escaped=true;uncertain=true;}}catch(_){uncertain=true;}
  if(p!==el){
   const b=p.getBoundingClientRect();
   if(!escaped&&/^(hidden|clip|scroll|auto)$/.test(c.overflowX)){left=Math.max(left,b.left);right=Math.min(right,b.right);}
   if(!escaped&&/^(hidden|clip|scroll|auto)$/.test(c.overflowY)){top=Math.max(top,b.top);bottom=Math.min(bottom,b.bottom);}
  }
  p=p.parentElement;depth++;
 }
 if(p)uncertain=true;
 if(right<=left||bottom<=top)return null;
 return {left,top,right,bottom,uncertain};
};
let el,scanned=0,truncated=false;
while((el=walk.nextNode())){
 if(scanned===256){truncated=true;break;} scanned++;
 const tag=el.tagName.toLowerCase();
 if(!/^(main|header|nav|section|aside|form|dialog|details|summary|ol|ul|li|p|span|h[1-6]|button|textarea)$/.test(tag)&&!el.hasAttribute('title'))continue;
 const r=el.getBoundingClientRect(),c=view.getComputedStyle(el);
 const visible=clippedRect(el);
 if(r.width<=0||r.height<=0||!visible)continue;
 if(nodes.length===48){truncated=true;break;}
 const clean=x=>String(x||'').replace(/\s+/g,' ').slice(0,160);
 const input=/^(input|textarea|select|option)$/.test(tag)||el.isContentEditable;
 const aria=el.getAttribute('aria-label'),title=el.getAttribute('title'),text=input?'':el.innerText;
	const name=clean(aria||title||text),name_source=aria?'aria_label':title?'title':text?'rendered_text':'empty';
 const raw=!input&&el.childElementCount===0?String(text||'').replace(/\s+/g,' '):'';
 const rendered_text=Array.from(raw.slice(0,322)).slice(0,160).join(''),text_truncated=raw.length>rendered_text.length;
 const round=x=>Math.round(x*100)/100;
 nodes.push({tag,id:clean(el.id),name,name_source,rendered_text,text_truncated,focused:el===this.activeElement,rect:{x:round(r.x),y:round(r.y),width:round(r.width),height:round(r.height)},visible_rect:{x:round(visible.left),y:round(visible.top),width:round(visible.right-visible.left),height:round(visible.bottom-visible.top)},visibility_uncertain:visible.uncertain,client_width:el.clientWidth,scroll_width:el.scrollWidth,white_space:c.whiteSpace,overflow_x:c.overflowX,text_overflow:c.textOverflow,tab_index:el.tabIndex});
}
return {title:this.title.slice(0,1024),text:s.slice(0,16384),truncated:s.length>16384,ready:this.readyState,layout:{viewport:{width:view.innerWidth,height:view.innerHeight},client_width:root.clientWidth,scroll_width:Math.max(root.scrollWidth,this.body?this.body.scrollWidth:0),client_height:root.clientHeight,scroll_height:Math.max(root.scrollHeight,this.body?this.body.scrollHeight:0),nodes,scanned,truncated,coverage:'Main-frame containers, paragraph/span text and titled text intersecting viewport and rectangular ancestor overflow clips; at most 256 scanned elements, 48 nodes, 32 ancestors per node. visible_rect does not prove lack of occlusion or pixel visibility. name_source=title/aria_label is a label, not displayed full text. rendered_text is leaf DOM innerText, omits editable/control values and is at most 160 Unicode characters; text_truncated marks its excerpt, not CSS clipping. innerText may contain characters clipped by CSS: assess that text node own client/scroll widths, white_space and visible_rect, with visibility_uncertain. focused=true observes activeElement now; omitted/offscreen nodes cannot prove lost focus. Absent/offscreen nodes, visibility_uncertain (including positioned/top-layer nodes), masks and shapes are not verified.'}};}`
