type MarkdownNode = {
  type: string;
  value?: string;
  url?: string;
  children?: MarkdownNode[];
  position?: { start: { offset?: number }; end: { offset?: number } };
};

// GFM includes CJK sentence punctuation in bare URLs. Split only autolinks
// identified from their source span; explicit Markdown links remain exact.
export function remarkCjkAutolinks() {
  return (tree: MarkdownNode, file: { value: unknown }) => {
    const source = String(file.value ?? "");
    const visit = (parent: MarkdownNode) => {
      if (!parent.children) return;
      parent.children = parent.children.flatMap((node) => {
        const label = node.children?.length === 1 && node.children[0].type === "text"
          ? node.children[0].value ?? "" : "";
        const start = node.position?.start.offset;
        const end = node.position?.end.offset;
        if (node.type === "link" && /^https?:\/\//iu.test(label) &&
          start !== undefined && end !== undefined && source.slice(start, end) === label) {
          const boundary = label.search(/[，。！？；：（）【】《》“”‘’、]/u);
          if (boundary >= 0) {
            const url = label.slice(0, boundary);
            return [{ ...node, url, children: [{ type: "text", value: url }] },
              { type: "text", value: label.slice(boundary) }];
          }
        }
        visit(node);
        return [node];
      });
    };
    visit(tree);
  };
}
