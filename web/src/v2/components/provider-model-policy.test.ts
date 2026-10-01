import { describe, expect, it } from "vitest";
import {
  inheritedProviderModelPolicy, providerModelGroups, providerModelPolicyError,
  providerPolicyControlsError, withProviderModelPolicy,
} from "./provider-model-policy";

describe("provider model planning policies", () => {
  it("uses one exact wire-model policy for mapped aliases without changing unrelated configuration", () => {
    const config = { model_mapping: { fast: "wire-fast", other: "wire-fast" },
      model_context_windows: { untouched: { window_tokens: 60000, default_output_tokens: 100, max_output_tokens: 200 } },
      model_capabilities: { fast: { vision: "supported" } }, extension: { keep: true } };
    expect(providerModelGroups(["fast", "other", "wire-fast"], config)).toEqual([
      { wireModel: "wire-fast", aliases: ["fast", "other", "wire-fast"] },
    ]);
    const policy = { window_tokens: 100000, default_output_tokens: 8000, max_output_tokens: 32000 };
    const updated = withProviderModelPolicy(config, "wire-fast", policy);
    expect(updated).toEqual({ ...config, model_context_windows: { ...config.model_context_windows, "wire-fast": policy } });
    expect(withProviderModelPolicy(updated, "wire-fast", null)).toEqual(config);
    expect(withProviderModelPolicy({ model_context_windows: { "wire-fast": policy } }, "wire-fast", null)).toEqual({});
  });

  it("does not silently rewrite malformed handwritten mappings or policies", () => {
    expect(providerPolicyControlsError({ model_mapping: { alias: 42 } })).toBeTruthy();
    expect(providerPolicyControlsError({ model_context_windows: [] })).toBeTruthy();
    expect(providerModelPolicyError({ model_context_windows: {} })).toBeTruthy();
    expect(providerModelPolicyError({ model_context_windows: { x: { window_tokens: 10000, default_output_tokens: 100, max_output_tokens: 1000, future: true } } })).toContain("未知字段");
  });

  it("treats prototype-like model IDs as ordinary keys", () => {
    const policy = { window_tokens: 32768, default_output_tokens: 1024, max_output_tokens: 4096 };
    const updated = withProviderModelPolicy({}, "__proto__", policy);
    expect(Object.hasOwn(updated.model_context_windows as object, "__proto__")).toBe(true);
    expect(JSON.parse(JSON.stringify(updated))).toEqual({ model_context_windows: { ["__proto__"]: policy } });
    expect(withProviderModelPolicy(updated, "__proto__", null)).toEqual({});
  });

  it.each([
    [4095, 100, 1000], [2097153, 100, 1000], [32768, 0, 4096], [32768, 4097, 4096],
    [32768, 100, 31744], [2097152, 100, 1000001], [32768, 1.5, 4096],
  ])("rejects invalid planning limits window=%s default=%s max=%s", (window, output, max) => {
    expect(providerModelPolicyError({ model_context_windows: { test: {
      window_tokens: window, default_output_tokens: output, max_output_tokens: max,
    } } })).toBeTruthy();
  });

  it("accepts the exact safety and adapter boundaries", () => {
    expect(providerModelPolicyError({ model_context_windows: { test: {
      window_tokens: 32768, default_output_tokens: 1, max_output_tokens: 31743,
    } } })).toBeUndefined();
    expect(providerModelPolicyError({ model_context_windows: { test: {
      window_tokens: 2097152, default_output_tokens: 1000000, max_output_tokens: 1000000,
    } } })).toBeUndefined();
  });

  it.each(["gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"])("shows runtime-aligned local defaults only for exact official OpenAI model %s", (model) => {
    const known = inheritedProviderModelPolicy("https://api.openai.com/v1/responses", "openai_responses", model, {});
    expect(known).toEqual({ source: "known", explicitDefault: true, policy: { window_tokens: 1050000, default_output_tokens: 16384, max_output_tokens: 128000 } });
    for (const endpoint of ["https://gateway.example/v1/responses", "https://api.openai.com.evil.example/v1/responses",
      "https://api.openai.com:8443/v1/responses", "https://api.openai.com/other", "http://api.openai.com/v1/responses",
      "https://api.openai.com/other/../v1/responses", "https://api.openai.com/responses"]) {
      expect(inheritedProviderModelPolicy(endpoint, "openai_responses", model, {}).source).toBe("fallback");
    }
    expect(inheritedProviderModelPolicy("https://api.openai.com/v1/responses", "openai_responses", `${model}-future`, {}).source).toBe("fallback");
  });

  it.each([
    [{}, 65536], [{ thinking: { type: "disabled" } }, 8192], [{ reasoning_effort: "max" }, 131072],
    [{ thinking: { type: "disabled" }, reasoning_effort: "max" }, 131072], [{ reasoning_effort: "none" }, 8192],
  ])("mirrors DeepSeek option precedence %j", (body, output) => {
    const result = inheritedProviderModelPolicy("https://api.deepseek.com/v1/chat/completions", "openai_chat_completions", "deepseek-v4-flash", { request_body: body });
    expect(result).toEqual({ source: "known", policy: { window_tokens: 1000000, default_output_tokens: output, max_output_tokens: 393216 } });
  });

  it("keeps unrecognized metadata and unsupported DeepSeek transports conservative", () => {
    for (const [endpoint, transport, model, config] of [
      ["https://api.deepseek.com/responses", "openai_responses", "deepseek-v4-flash", {}],
      ["https://api.deepseek.com/v1/chat/completions", "openai_chat_completions", "deepseek-v4-flash", { request_body: { reasoning_effort: "future" } }],
      ["https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", "openai_chat_completions", "gemini-4", {}],
    ] as const) {
      expect(inheritedProviderModelPolicy(endpoint, transport, model, config)).toEqual({ source: "fallback", policy: {
        window_tokens: 32768, default_output_tokens: 1024, max_output_tokens: 4096,
      } });
    }
  });
});
