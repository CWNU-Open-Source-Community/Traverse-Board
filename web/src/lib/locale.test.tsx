import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LocaleProvider, readPrayuLocale, useLocale } from "./locale";

function Probe() {
  const { locale, setLocale, t } = useLocale();
  return <div><span>{locale}</span><strong>{t("中文界面", "English interface")}</strong>
    <button onClick={() => setLocale("en-US")} type="button">switch</button></div>;
}

describe("Prayu locale", () => {
  beforeEach(() => localStorage.clear());

  it("defaults to Chinese and persists an explicit English selection", async () => {
    const user = userEvent.setup();
    render(<LocaleProvider><Probe /></LocaleProvider>);

    expect(screen.getByText("zh-CN")).toBeInTheDocument();
    expect(screen.getByText("中文界面")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "switch" }));
    expect(screen.getByText("en-US")).toBeInTheDocument();
    expect(screen.getByText("English interface")).toBeInTheDocument();
    expect(localStorage.getItem("prayu.locale.v1")).toBe("en-US");
    expect(document.documentElement.lang).toBe("en-US");
    expect(readPrayuLocale()).toBe("en-US");
  });
});
