import { render, screen } from "@testing-library/react";
import { SafeMarkdown } from "./safe-markdown";

describe("SafeMarkdown", () => {
  it("renders GFM prose instead of exposing Markdown source", () => {
    render(<SafeMarkdown>{"## 功能\n\n**已完成**\n\n- 一\n- 二\n\n`go test`"}</SafeMarkdown>);

    expect(screen.getByRole("heading", { name: "功能", level: 2 })).toBeInTheDocument();
    expect(screen.getByText("已完成").tagName).toBe("STRONG");
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getByText("go test").tagName).toBe("CODE");
  });

  it("drops raw HTML and keeps unsafe or local links inert", () => {
    const { container } = render(<SafeMarkdown>{
      "[unsafe](javascript:alert(1)) [local](file:///secret) [web](https://example.com)\n\n<script>alert(1)</script>"
    }</SafeMarkdown>);

    expect(container.querySelector("script")).not.toBeInTheDocument();
    expect(screen.getByText("unsafe").tagName).toBe("SPAN");
    expect(screen.getByText("local").tagName).toBe("SPAN");
    expect(screen.getByRole("link", { name: "web" })).toHaveAttribute("rel", "noreferrer noopener");
  });

  it("does not create remote image requests", () => {
    const { container } = render(<SafeMarkdown>{"![tracking](https://example.com/pixel.png)"}</SafeMarkdown>);
    expect(container.querySelector("img")).not.toBeInTheDocument();
  });

  it("keeps Chinese source annotations outside bare URL targets", () => {
    const { container } = render(<SafeMarkdown>{
      "Zod 官方来源 https://zod.dev/basics（供应商检索来源）；Node.js 官方来源 https://nodejs.org/api/test.html（供应商检索来源）。"
    }</SafeMarkdown>);
    expect(screen.getAllByRole("link").map((link) => link.getAttribute("href"))).toEqual([
      "https://zod.dev/basics", "https://nodejs.org/api/test.html",
    ]);
    expect(container).toHaveTextContent("（供应商检索来源）；Node.js");
    expect(container).toHaveTextContent("（供应商检索来源）。");
  });

  it("preserves explicit destinations, Unicode paths and code text", () => {
    render(<SafeMarkdown>{
      "[原始地址](https://example.com/路径（版本）) https://example.com/中文路径 `https://example.com/（示例）`"
    }</SafeMarkdown>);
    expect(decodeURI(screen.getByRole("link", { name: "原始地址" }).getAttribute("href")!)).toBe("https://example.com/路径（版本）");
    expect(decodeURI(screen.getByRole("link", { name: "https://example.com/中文路径" }).getAttribute("href")!)).toBe("https://example.com/中文路径");
    expect(screen.getByText("https://example.com/（示例）").tagName).toBe("CODE");
  });
});
