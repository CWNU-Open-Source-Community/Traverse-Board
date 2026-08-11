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
});
