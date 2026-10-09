import { useEffect, useState } from "react";
import type { APIClient } from "../../api/client";
import type { RunView, TaskBudgetSettings, TaskConfigurationView } from "../../api/types";
import { normalizedTaskBudget } from "../../api/task-configuration";
import "./task-configuration.css";

export interface TaskConfigurationProps {
  client: APIClient;
  workspaceID: string;
  profile?: "code" | "review" | "learn" | "script";
  budget?: TaskBudgetSettings;
  onBudgetChange?: (budget: TaskBudgetSettings | undefined) => void;
  onValidityChange?: (valid: boolean) => void;
  run?: RunView | null;
  disabled?: boolean;
}

const fields = [
  { key: "max_turns", label: "回合上限", min: 1, max: 10_000, placeholder: "默认 100", step: "1" },
  { key: "max_tool_calls", label: "工具调用上限", min: 1, max: 1_000_000, placeholder: "默认 100", step: "1" },
  { key: "max_tokens", label: "Token 上限", min: 0, max: 1_000_000_000, placeholder: "0 表示不限制", step: "1" },
  { key: "max_cost_usd", label: "费用上限（USD）", min: 0, max: 100_000, placeholder: "0 表示不限制", step: "0.000001" },
  { key: "timeout_seconds", label: "时长上限（秒）", min: 0, max: 604_800, placeholder: "0 表示不限制", step: "1" },
] as const;
const sourceLabels: Record<string, string> = { default: "产品默认", operator: "任务设置", project: "项目收窄", snapshot: "已有执行快照" };
const fieldLabels: Record<string, string> = {
  ...Object.fromEntries(fields.map((field) => [`budget.${field.key}`, field.label])),
  read_only: "项目只读", allowed_profiles: "允许的任务类型", exclude_paths: "排除路径", skill_suggestions: "技能建议",
  test_command_id: "测试动作", format_command_id: "格式化动作", project_config: "项目配置文件",
};
type ReadState = { key: string; client: APIClient; view?: TaskConfigurationView; error?: string; loading?: boolean };

// Go owns effective configuration and admission. This component only edits
// bounded draft inputs and presents safe read projections; previews grant no authority.
export function TaskConfiguration({ client, workspaceID, profile = "code", budget, onBudgetChange, onValidityChange, run, disabled }: TaskConfigurationProps) {
  const [refresh, setRefresh] = useState(0);
  const [state, setState] = useState<ReadState>({ key: "", client });
  const requestKey = JSON.stringify([run?.id ?? "", workspaceID, profile, budget ?? {}, refresh]);
  let inputError = "";
  try { if (!run) normalizedTaskBudget(budget); } catch (error) { inputError = error instanceof Error ? error.message : "预算无效。"; }
  const current = state.key === requestKey && state.client === client ? state : undefined;
  const view = current?.view;
  const valid = !inputError && (!view || view.project_disposition !== "rejected");
  useEffect(() => { onValidityChange?.(valid); }, [onValidityChange, valid]);

  useEffect(() => {
    if (!workspaceID || inputError) return;
    const abort = new AbortController();
    setState({ key: requestKey, client, loading: true });
    // Debounce draft edits; a task change aborts the old read and hides its view
    // immediately, even when a transport cannot cancel its pending response.
    const timer = setTimeout(() => {
      const read = run ? client.getRunTaskConfiguration(run.id, abort.signal)
        : client.previewTaskConfiguration({ workspace_id: workspaceID, profile, budget }, abort.signal);
      void read.then((result) => {
        if (!abort.signal.aborted) setState({ key: requestKey, client, view: result });
      }, (error: unknown) => {
        if (!abort.signal.aborted) setState({ key: requestKey, client, error: error instanceof Error ? error.message : "无法读取任务配置。" });
      });
    }, run ? 0 : 200);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [client, requestKey, workspaceID, profile, run?.id, inputError]);

  function change(key: keyof TaskBudgetSettings, text: string, badInput: boolean) {
    const next = { ...budget };
    if (badInput) next[key] = Number.NaN; else if (text === "") delete next[key]; else next[key] = Number(text);
    onBudgetChange?.(Object.keys(next).length ? next : undefined);
  }

  return <section className="v2-task-configuration" aria-label="任务预算与项目配置">
    <header><h2>任务预算与项目配置</h2><button type="button" disabled={!workspaceID || Boolean(inputError) || current?.loading} onClick={() => setRefresh((value) => value + 1)}>重新读取</button></header>
    <p>{run ? "当前执行使用创建时保存的配置。项目文件后续修改不会改变此快照。" : "设置本次任务的执行上限；项目配置可以进一步收窄。"}</p>
    {!run && onBudgetChange ? <fieldset disabled={disabled} className="v2-task-budget-fields"><legend>预算设置</legend>{fields.map((field) => <label key={field.key}><span>{field.label}</span>
      <input aria-label={field.label} type="number" min={field.min} max={field.max} step={field.step} placeholder={field.placeholder}
        value={Number.isNaN(budget?.[field.key]) ? "" : budget?.[field.key] ?? ""} onChange={(event) => change(field.key, event.target.value, event.target.validity.badInput)} />
    </label>)}<button type="button" onClick={() => onBudgetChange(undefined)}>恢复默认预算</button></fieldset> : null}
    <p className="v2-task-configuration-help">限制作用于每次执行；后续执行继承相同上限。费用上限需要有效价格快照，它是预算限制，并非精确账单。项目中的动作和技能建议不会在预览时执行。</p>
    {!workspaceID ? <p role="status">选择工作区后可读取项目配置。</p> : inputError ? <p role="alert">{inputError}</p> : current?.error ? <p role="alert">{current.error}</p> : !view ? <p role="status">正在读取配置…</p> : <>
      {view.project_disposition === "rejected" ? <div role="alert"><strong>项目配置已拒绝，无法创建任务。</strong><ul>{view.rejections.map((rejection, index) => <li key={`${rejection.field}-${index}`}>{fieldLabels[rejection.field] ?? rejection.field}：{rejection.reason}</li>)}</ul><p>请修正 .prayu/config.yaml 后重新读取。</p></div> : <>
        <h3>{run ? "已保存的执行上限" : "生效的执行上限"}</h3><dl>{fields.map((field) => <div key={field.key}><dt>{field.label}</dt><dd>{view.budget[field.key] || "不限制"}<small>{sourceLabels[view.sources.find((source) => source.field === `budget.${field.key}`)?.source ?? "snapshot"]}</small></dd></div>)}</dl>
        <h3>项目配置</h3><p>{view.project_disposition === "absent" ? "未发现项目配置，使用任务设置与产品默认值。" : "已应用 .prayu/config.yaml 的预算与任务类型收窄设置。"}</p>
        {view.project ? <dl><div><dt>项目只读</dt><dd>{view.project.read_only ? "已开启" : "未额外限制"}</dd></div><div><dt>允许的任务类型</dt><dd>{view.project.allowed_profiles.length ? view.project.allowed_profiles.join("、") : "未额外限制"}</dd></div><div><dt>排除路径</dt><dd>{view.project.excluded_path_count} 项<small>已记录，当前不会自动排除访问</small></dd></div><div><dt>技能建议</dt><dd>{view.project.skill_suggestion_count} 项<small>已记录，未安装或启用</small></dd></div>
          {view.project.test_command_id ? <div><dt>测试动作</dt><dd>{view.project.test_command_id}<small>已记录，未执行</small></dd></div> : null}{view.project.format_command_id ? <div><dt>格式化动作</dt><dd>{view.project.format_command_id}<small>已记录，未执行</small></dd></div> : null}</dl> : null}
        <details><summary>字段来源与配置指纹</summary><ul>{view.sources.map((source) => <li key={source.field}>{fieldLabels[source.field]}：{sourceLabels[source.source]}</li>)}</ul><p>配置指纹 <code>{view.fingerprint}</code></p>{view.project_fingerprint ? <p>项目快照指纹 <code>{view.project_fingerprint}</code></p> : null}</details>
      </>}
    </>}
  </section>;
}
