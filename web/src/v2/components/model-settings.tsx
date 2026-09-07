import { useEffect, useRef, useState, type ReactNode } from "react";
import { ArrowLeft, Settings2 } from "lucide-react";
import type { CyberAgentClient } from "../../api/client";
import { V2ProviderSettings } from "./provider-settings";

export type V2ModelProviderPreset = {
  id: string;
  providerName: string;
  modelName: string;
  icon: ReactNode;
  /** True only after the OS credential store reports a configured secret. */
  credentialConfigured?: boolean;
  /** Account-based presets (for example GitHub Copilot) must not be described as API-key routes. */
  setup?:
    | { kind: "api_key"; configured: boolean }
    | { kind: "account"; connected: boolean; accountName?: string };
  /** Static palette hint. CSP-safe product CSS maps the preset ID to the actual icon-well tint. */
  accent?: string;
};

export type V2ModelSettingsProps = {
  client: CyberAgentClient;
  presets: readonly V2ModelProviderPreset[];
  onSelectPreset: (preset: V2ModelProviderPreset, trigger: HTMLButtonElement) => void;
};

function setupStateLabel(preset: V2ModelProviderPreset): string {
  if (preset.setup?.kind === "account") {
    const account = preset.setup.accountName?.trim() || "账户";
    return preset.setup.connected ? `${account}已连接` : `尚未连接 ${account}`;
  }
  const configured = preset.setup?.kind === "api_key"
    ? preset.setup.configured : Boolean(preset.credentialConfigured);
  return configured ? "已保存 API Key" : "尚未保存 API Key";
}

export function V2ModelSettings({ client, presets, onSelectPreset }: V2ModelSettingsProps) {
  const [view, setView] = useState<"catalog" | "custom">("catalog");
  const customTriggerRef = useRef<HTMLButtonElement>(null);
  const backButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (view !== "custom") return;
    const frame = requestAnimationFrame(() => backButtonRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [view]);

  const returnToCatalog = () => {
    setView("catalog");
    requestAnimationFrame(() => customTriggerRef.current?.focus());
  };

  if (view === "custom") {
    return <>
      <button aria-label="返回模型选择" className="v2-model-custom-back"
        onClick={returnToCatalog} ref={backButtonRef} type="button">
        <ArrowLeft aria-hidden="true" size={16} />返回模型
      </button>
      <V2ProviderSettings client={client} />
    </>;
  }

  return <section aria-labelledby="v2-model-settings-title">
    <header className="v2-model-catalog-heading">
      <h1 id="v2-model-settings-title">模型</h1>
      <p>选择预设供应商，或使用高级 JSON 接入兼容接口。</p>
    </header>
    <ul aria-label="模型供应商" className="v2-model-catalog" role="list">
      <li>
        <button aria-label="打开自定义模型配置" className="v2-model-card is-custom"
          onClick={() => setView("custom")} ref={customTriggerRef} type="button">
          <span aria-hidden="true" className="v2-model-card-icon"><Settings2 /></span>
          <span className="v2-model-card-copy"><strong>自定义配置</strong>
            <span>自定义模型</span></span>
        </button>
      </li>
      {presets.map((preset) => {
        const setupState = setupStateLabel(preset);
        return <li key={preset.id}>
          <button aria-label={`${preset.providerName}，${preset.modelName}，${setupState}`}
            className="v2-model-card" onClick={(event) => onSelectPreset(preset, event.currentTarget)}
            data-model-provider-id={preset.id} type="button">
            <span aria-hidden="true" className="v2-model-card-icon">{preset.icon}</span>
            <span className="v2-model-card-copy"><strong>{preset.providerName}</strong>
              <span>{preset.modelName}</span></span>
          </button>
        </li>;
      })}
    </ul>
  </section>;
}
