import type { ReactNode } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

const allowedElements = [
  "a", "blockquote", "br", "code", "del", "em", "h1", "h2", "h3", "h4", "h5", "h6",
  "hr", "li", "ol", "p", "pre", "strong", "table", "tbody", "td", "th", "thead", "tr", "ul",
];

export function SafeMarkdown({ children, className = "" }: {
  children: string;
  className?: string;
}) {
  return (
    <div className={`safe-markdown ${className}`.trim()}>
      <Markdown allowedElements={allowedElements} components={{
        a: ({ children: label, href }) => <SafeLink href={href}>{label}</SafeLink>,
      }} remarkPlugins={[remarkGfm]} skipHtml>
        {children}
      </Markdown>
    </div>
  );
}

function SafeLink({ children, href }: { children: ReactNode; href?: string }) {
  const normalized = href?.trim() ?? "";
  if (!/^https?:\/\//iu.test(normalized)) {
    return <span className="safe-markdown-inert-link">{children}</span>;
  }
  return <a href={normalized} rel="noreferrer noopener" target="_blank">{children}</a>;
}
