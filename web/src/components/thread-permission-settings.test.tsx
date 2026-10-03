import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { CyberAgentClient } from "../api/client";
import { ThreadPermissionSettings } from "./thread-permission-settings";

vi.mock("../lib/locale", () => ({
  useLocale: () => ({ locale: "zh-CN", setLocale: () => undefined,
    t: (chinese: string) => chinese }),
}));

describe("ThreadPermissionSettings", () => {
  it("uses the shared selector's empty state without reading or writing a preference", () => {
    const client = new CyberAgentClient("read");
    const get = vi.spyOn(client, "get");
    const queryClient = new QueryClient();
    render(<QueryClientProvider client={queryClient}><ThreadPermissionSettings client={client} threadID="" /></QueryClientProvider>);
    expect(screen.getByText("先从侧栏打开一个对话。")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });
});
