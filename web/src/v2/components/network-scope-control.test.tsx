import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { V2NetworkScopeControl } from "./network-scope-control";

describe("V2NetworkScopeControl", () => {
  it("requires an explicit confirmation before granting a bounded HTTPS allowlist", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<V2NetworkScopeControl mode="disabled" onChange={onChange} targets={[]} />);

    await user.click(screen.getByRole("button", { name: /无网络/u }));
    await user.type(screen.getByRole("textbox", { name: "允许访问的 HTTPS 目标" }),
      "docs.example.com\nhttps://search.example.org");
    await user.click(screen.getByRole("button", { name: /使用白名单/u }));

    expect(onChange).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "启用网页访问？" });
    await user.click(within(dialog).getByRole("button", { name: "允许这些目标" }));
    expect(onChange).toHaveBeenCalledWith("allowlist", [
      "docs.example.com", "search.example.org",
    ]);
  });

  it("canonicalizes equivalent HTTPS origins before granting them", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<V2NetworkScopeControl mode="disabled" onChange={onChange} targets={[]} />);

    await user.click(screen.getByRole("button", { name: /无网络/u }));
    await user.type(screen.getByRole("textbox", { name: "允许访问的 HTTPS 目标" }),
      "HTTPS://Docs.Example.COM:443/\ndocs.example.com");
    await user.click(screen.getByRole("button", { name: /使用白名单/u }));
    await user.click(within(screen.getByRole("dialog", { name: "启用网页访问？" }))
      .getByRole("button", { name: "允许这些目标" }));

    expect(onChange).toHaveBeenCalledWith("allowlist", ["docs.example.com"]);
  });

  it("rejects wildcard, path-bearing, and non-HTTPS targets before submission", async () => {
    const user = userEvent.setup();
    render(<V2NetworkScopeControl mode="disabled" onChange={vi.fn()} targets={[]} />);

    await user.click(screen.getByRole("button", { name: /无网络/u }));
    await user.type(screen.getByRole("textbox", { name: "允许访问的 HTTPS 目标" }),
      "*.example.com\nhttp://example.org\nhttps://example.net/path\nlocalhost\n192.168.1.2");

    expect(screen.getByRole("alert")).toHaveTextContent("只接受无路径、查询或通配符");
    expect(screen.getByRole("button", { name: /使用白名单/u })).toBeDisabled();
  });
});
