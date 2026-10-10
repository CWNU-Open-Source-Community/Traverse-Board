import { useEffect, useState } from "react";
import type { APIClient } from "../../api/client";
import type { RunView, TaskBudgetSettings, TaskConfigurationView } from "../../api/types";
import { normalizedTaskBudget } from "../../api/task-configuration";
import { useLocale } from "../../lib/locale";
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
  { key: "max_turns", label: ["回合上限", "Turn limit"], min: 1, max: 10_000, placeholder: ["默认 100", "Default 100"], step: "1" },
  { key: "max_tool_calls", label: ["工具调用上限", "Tool call limit"], min: 1, max: 1_000_000, placeholder: ["默认 100", "Default 100"], step: "1" },
  { key: "max_tokens", label: ["Token 上限", "Token limit"], min: 0, max: 1_000_000_000, placeholder: ["0 表示不限制", "0 means unlimited"], step: "1" },
  { key: "max_cost_usd", label: ["费用上限（USD）", "Cost limit (USD)"], min: 0, max: 100_000, placeholder: ["0 表示不限制", "0 means unlimited"], step: "0.000001" },
  { key: "timeout_seconds", label: ["时长上限（秒）", "Duration limit (seconds)"], min: 0, max: 604_800, placeholder: ["0 表示不限制", "0 means unlimited"], step: "1" },
] as const;
const errorEnglish: Record<string, string> = {
  "预算字段无效。": "Invalid budget fields.", "预算字段必须为有限数字。": "Budget fields must be finite numbers.",
  "预算超出支持范围。": "Budget exceeds the supported range.", "费用上限至少为 0.000001 USD。": "The cost limit must be at least 0.000001 USD.",
  "预算无效。": "Invalid budget.", "无法读取任务配置。": "Unable to read task configuration.",
  "任务配置响应的格式或来源不匹配。": "Task configuration response format or identity does not match.",
  "任务配置字段来源无效。": "Invalid task configuration field source.", "任务预算绑定无效。": "Invalid task budget binding.",
  "任务配置拒绝记录无效。": "Invalid task configuration rejection record.", "拒绝的配置包含部分生效记录。": "Rejected configuration contains partial effective records.",
  "项目配置投影无效。": "Invalid project configuration projection.", "缺失项目配置却包含项目快照。": "A project snapshot is present without project configuration.",
};
type ReadState = { key: string; client: APIClient; view?: TaskConfigurationView; error?: string; loading?: boolean };

// Go owns effective configuration and admission. This component only edits
// bounded draft inputs and presents safe read projections; previews grant no authority.
export function TaskConfiguration({ client, workspaceID, profile = "code", budget, onBudgetChange, onValidityChange, run, disabled }: TaskConfigurationProps) {
  const { t, locale } = useLocale();
  const localizedFields = fields.map((field) => ({ ...field, label: t(field.label[0], field.label[1]), placeholder: t(field.placeholder[0], field.placeholder[1]) }));
  const sourceLabels: Record<string, string> = { default: t("产品默认", "Product defaults"), operator: t("任务设置", "Task settings"), project: t("项目收窄", "Project restrictions"), snapshot: t("已有执行快照", "Existing run snapshot") };
  const fieldLabels: Record<string, string> = {
    ...Object.fromEntries(localizedFields.map((field) => [`budget.${field.key}`, field.label])),
    read_only: t("项目只读", "Read-only project"), allowed_profiles: t("允许的任务类型", "Allowed task types"), exclude_paths: t("排除路径建议", "Suggested excluded paths"), skill_suggestions: t("技能建议", "Skill suggestions"),
    test_command_id: t("测试动作", "Test action"), format_command_id: t("格式化动作", "Format action"), project_config: t("项目配置文件", "Project configuration file"),
  };
  const errorLabel = (message: string) => t(message, errorEnglish[message] ?? message);
  const [refresh, setRefresh] = useState(0);
  const [state, setState] = useState<ReadState>({ key: "", client });
  const requestKey = JSON.stringify([run?.id ?? "", workspaceID, profile, budget ?? {}, refresh]);
  let inputError = "";
  try { if (!run) normalizedTaskBudget(budget); } catch (error) { inputError = error instanceof Error ? error.message : "预算无效。"; }
  const current = state.key === requestKey && state.client === client ? state : undefined;
  const view = current?.view;
  useEffect(() => {
    if (inputError) onValidityChange?.(false);
    else if (view) onValidityChange?.(view.project_disposition !== "rejected");
    // An unfinished or failed read cannot clear a known project rejection.
  }, [onValidityChange, inputError, view]);

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

  return <section className="v2-task-configuration" aria-label={t("任务预算与项目配置", "Task budget and project configuration")}>
    <header><h2>{run ? t("本次执行配置", "Configuration for this run") : t("执行预算", "Execution budget")}</h2><button type="button" disabled={!workspaceID || Boolean(inputError) || current?.loading} onClick={() => setRefresh((value) => value + 1)}>{t("重新读取", "Refresh")}</button></header>
    <p>{run ? t("预算和项目配置以创建此执行时保存的记录为准。", "The budget and project configuration are the records saved when this run was created.") : t("设置任务的执行上限，下方预览包含项目配置中的限制。", "Set execution limits for the task. The preview below includes restrictions from project configuration.")}</p>
    {!run && onBudgetChange ? <fieldset disabled={disabled} className="v2-task-budget-fields"><legend>{t("预算设置", "Budget settings")}</legend>{localizedFields.map((field) => <label key={field.key}><span>{field.label}</span>
      <input aria-label={field.label} type="number" min={field.min} max={field.max} step={field.step} placeholder={field.placeholder}
        value={Number.isNaN(budget?.[field.key]) ? "" : budget?.[field.key] ?? ""} onChange={(event) => change(field.key, event.target.value, event.target.validity.badInput)} />
    </label>)}<button type="button" onClick={() => onBudgetChange(undefined)}>{t("恢复默认预算", "Restore default budget")}</button></fieldset> : null}
    <p className="v2-task-configuration-help">{t("每次执行沿用这些上限。费用上限按有效的模型价格快照估算，实际费用请查看供应商账单。", "Each run inherits these limits. Cost limits are estimated from a valid model price snapshot; check your provider's bill for actual charges.")}</p>
    {!workspaceID ? <p role="status">{t("选择工作区后可读取项目配置。", "Select a workspace to read project configuration.")}</p> : inputError ? <p role="alert">{errorLabel(inputError)}</p> : current?.error ? <p role="alert">{errorLabel(current.error)}</p> : !view ? <p role="status">{t("正在读取配置…", "Reading configuration…")}</p> : <>
      {view.project_disposition === "rejected" ? <div role="alert"><strong>{t("项目配置已拒绝，无法创建任务。", "Project configuration was rejected; task creation is blocked.")}</strong><ul>{view.rejections.map((rejection, index) => <li key={`${rejection.field}-${index}`}>{fieldLabels[rejection.field] ?? rejection.field}{t("：", ": ")}{rejection.reason}</li>)}</ul><p>{t("请修正 .prayu/config.yaml 后重新读取。", "Fix .prayu/config.yaml, then refresh.")}</p></div> : <>
        <h3>{run ? t("已保存的执行上限", "Saved execution limits") : t("生效的执行上限", "Effective execution limits")}</h3><dl>{localizedFields.map((field) => <div key={field.key}><dt>{field.label}</dt><dd>{view.budget[field.key] || t("不限制", "Unlimited")}<small>{sourceLabels[view.sources.find((source) => source.field === `budget.${field.key}`)?.source ?? "snapshot"]}</small></dd></div>)}</dl>
        <h3>{t("项目配置", "Project configuration")}</h3><p>{view.project_disposition === "absent" ? view.sources.some((source) => source.source === "snapshot") ? t("未保存项目配置，使用已有执行快照。", "No project configuration is saved; the existing run snapshot applies.") : t("未发现项目配置，使用任务设置与产品默认值。", "No project configuration was found; task settings and product defaults apply.") : t("已应用 .prayu/config.yaml 的预算与任务类型收窄设置。", "Budget and task type restrictions from .prayu/config.yaml have been applied.")}</p>
        {view.sources.some((source) => source.source === "snapshot") ? <p>{t("历史执行沿用保存的上限；工具调用上限为 0 或省略时表示不限次数。", "Historical runs use their saved limits; a zero or omitted tool limit means unlimited.")}</p> : null}
        {view.project ? <dl><div><dt>{fieldLabels.read_only}</dt><dd>{view.project.read_only ? t("已开启", "Enabled") : t("未额外限制", "No additional restriction")}</dd></div><div><dt>{fieldLabels.allowed_profiles}</dt><dd>{view.project.allowed_profiles.length ? view.project.allowed_profiles.join(locale === "zh-CN" ? "、" : ", ") : t("未额外限制", "No additional restriction")}</dd></div><div><dt>{fieldLabels.exclude_paths}</dt><dd>{view.project.excluded_path_count}{t(" 项", " items")}<small>{t("供参考，访问范围按任务权限处理", "Suggestions only; task permissions govern access")}</small></dd></div><div><dt>{fieldLabels.skill_suggestions}</dt><dd>{view.project.skill_suggestion_count}{t(" 项", " items")}<small>{t("已记录，待安装或启用", "Recorded; awaiting installation or activation")}</small></dd></div>
          {view.project.test_command_id ? <div><dt>{fieldLabels.test_command_id}</dt><dd>{view.project.test_command_id}<small>{t("已记录，未执行", "Recorded; not executed")}</small></dd></div> : null}{view.project.format_command_id ? <div><dt>{fieldLabels.format_command_id}</dt><dd>{view.project.format_command_id}<small>{t("已记录，未执行", "Recorded; not executed")}</small></dd></div> : null}</dl> : null}
        <details><summary>{t("字段来源与配置指纹", "Field sources and configuration fingerprints")}</summary><ul>{view.sources.map((source) => <li key={source.field}>{fieldLabels[source.field]}{t("：", ": ")}{sourceLabels[source.source]}</li>)}</ul><p>{t("配置指纹", "Configuration fingerprint")} <code>{view.fingerprint}</code></p>{view.project_fingerprint ? <p>{t("项目快照指纹", "Project snapshot fingerprint")} <code>{view.project_fingerprint}</code></p> : null}</details>
      </>}
    </>}
  </section>;
}
