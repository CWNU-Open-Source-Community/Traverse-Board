import { render, screen } from "@testing-library/react";
import { LocaleProvider } from "../lib/locale";
import { PrayuBrand } from "./prayu-brand";

describe("PrayuBrand", () => {
  beforeEach(() => window.localStorage.clear());

  it("marks the bilingual hero by script and keeps a localized tagline", () => {
    window.localStorage.setItem("prayu.locale.v1", "zh-CN");
    const { container } = render(<LocaleProvider><PrayuBrand variant="hero" /></LocaleProvider>);

    expect(screen.getByRole("img", { name: "Traverse Board · 针路簿" })).toBeInTheDocument();
    expect(container.querySelector('.prayu-brand-name-latin[lang="en"]')).toHaveTextContent("Traverse Board");
    expect(container.querySelector('.prayu-brand-name-cjk[lang="zh-CN"]')).toHaveTextContent("针路簿");
    expect(container.querySelector('small span[lang="en"]')).toHaveTextContent("Agent");
    expect(container.querySelector('small span[lang="zh-CN"]')).toHaveTextContent("工作台");
  });

  it("uses the active locale and script role for the compact name", () => {
    window.localStorage.setItem("prayu.locale.v1", "en-US");
    const { container } = render(<LocaleProvider><PrayuBrand /></LocaleProvider>);

    expect(screen.getByRole("img", { name: "Traverse Board" })).toBeInTheDocument();
    expect(container.querySelector('.prayu-brand-name-latin[lang="en-US"]')).toHaveTextContent("Traverse Board");
    expect(container.querySelector(".prayu-brand-name-cjk")).not.toBeInTheDocument();
    expect(container.querySelector("small")).toHaveTextContent("Agent Workbench");
  });

  it("renders icon-only surfaces without hidden display copy", () => {
    render(<PrayuBrand variant="icon" />);

    const brand = screen.getByRole("img", { name: "Traverse Board" });
    expect(brand.querySelector(".prayu-brand-copy")).not.toBeInTheDocument();
  });
});
