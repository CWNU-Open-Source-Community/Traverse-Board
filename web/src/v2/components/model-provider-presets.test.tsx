import { isValidElement } from "react";
import { describe, expect, it } from "vitest";
import { v2ModelProviderPresets } from "./model-provider-presets";

describe("v2 model provider presets", () => {
  it("keeps the requested eleven providers in product order", () => {
    expect(v2ModelProviderPresets).toHaveLength(11);
    expect(v2ModelProviderPresets.map((preset) => preset.providerName)).toEqual([
      "Claude",
      "OpenAI",
      "DeepSeek",
      "Gemini",
      "Grok",
      "MiniMax",
      "MiMo",
      "Kimi",
      "Kimi for Coding",
      "OpenCode Go",
      "GitHub Copilot",
    ]);
    expect(v2ModelProviderPresets.every((preset) => isValidElement(preset.icon))).toBe(true);
  });

  it("uses exact official endpoints and supported transports", () => {
    const byID = new Map(v2ModelProviderPresets.map((preset) => [preset.id, preset]));
    expect(byID.get("official-anthropic")?.draft).toMatchObject({
      endpointURL: "https://api.anthropic.com/v1/messages",
      transport: "anthropic_messages",
      defaultModel: "claude-sonnet-5",
    });
    expect(byID.get("official-openai")?.draft).toMatchObject({
      endpointURL: "https://api.openai.com/v1/responses",
      transport: "openai_responses",
      defaultModel: "gpt-5.6-terra",
      searchMode: "provider_native",
      nativeSearchDeclared: true,
    });
    expect(byID.get("official-deepseek")?.draft).toMatchObject({
      endpointURL: "https://api.deepseek.com/responses",
      transport: "openai_responses",
      models: ["deepseek-v4-flash", "deepseek-v4-pro"],
      searchMode: "provider_native",
      nativeSearchDeclared: true,
      advancedConfig: { request_body: { reasoning: { effort: "none" } } },
    });
    expect(byID.get("official-google-gemini")?.draft).toMatchObject({
      endpointURL: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
      transport: "openai_chat_completions",
      defaultModel: "gemini-3.7-flash",
    });
    expect(byID.get("official-xai")?.draft).toMatchObject({
      endpointURL: "https://api.x.ai/v1/responses",
      transport: "openai_responses",
      defaultModel: "grok-4.6",
      searchMode: "provider_native",
      nativeSearchDeclared: true,
    });
    expect(byID.get("official-minimax")?.draft).toMatchObject({
      endpointURL: "https://api.minimax.io/v1/responses",
      defaultModel: "MiniMax-M3",
    });
    expect(byID.get("official-mimo")?.draft).toMatchObject({
      endpointURL: "https://api.xiaomimimo.com/v1/responses",
      models: ["mimo-v2.5-pro", "mimo-v2.5"],
    });
    expect(byID.get("opencode-go")?.draft).toMatchObject({
      endpointURL: "https://opencode.ai/zen/go/v1/responses",
      transport: "openai_responses",
      defaultModel: "gpt-5.6-luna",
    });
  });

  it("keeps Kimi platform and Kimi for Coding as separate credentials and routes", () => {
    const kimi = v2ModelProviderPresets.find((preset) => preset.id === "official-kimi");
    const coding = v2ModelProviderPresets.find((preset) => preset.id === "official-kimi-coding");

    expect(kimi).toMatchObject({ kind: "api_key", modelName: "kimi-k3" });
    expect(kimi?.draft).toMatchObject({
      endpointURL: "https://api.moonshot.ai/v1/chat/completions",
      models: ["kimi-k3"],
    });
    expect(coding).toMatchObject({ kind: "api_key", modelName: "kimi-for-coding" });
    expect(coding?.draft).toMatchObject({
      endpointURL: "https://api.kimi.com/coding/v1/chat/completions",
      models: ["kimi-for-coding", "k3", "k3-256k", "kimi-for-coding-highspeed"],
      advancedConfig: { request_headers: { "User-Agent": "Traverse-Board" } },
    });
    expect(kimi?.draft?.id).not.toBe(coding?.draft?.id);
  });

  it("models Copilot only as an account connector, never an API-key provider", () => {
    const copilot = v2ModelProviderPresets.at(-1);

    expect(copilot).toMatchObject({
      id: "github-copilot",
      providerName: "GitHub Copilot",
      modelName: "连接后选择模型",
      kind: "account",
      setup: { kind: "account", connected: false, accountName: "GitHub 账户" },
    });
    expect(copilot?.draft).toBeUndefined();
    expect(v2ModelProviderPresets.slice(0, -1).every((preset) =>
      preset.kind === "api_key" && preset.draft !== undefined)).toBe(true);
  });
});
