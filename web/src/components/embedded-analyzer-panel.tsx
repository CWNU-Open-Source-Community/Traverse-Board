import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Binary, FileText, LoaderCircle, ShieldCheck } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type { EmbeddedAnalyzerExecutionControlView } from "../api/types";
import { useLocale } from "../lib/locale";
import { ErrorState } from "./common";

type InputMode = "text" | "file";

export function EmbeddedAnalyzerPanel({ client, runID }: {
  client: CyberAgentClient;
  runID: string;
}) {
  const { t } = useLocale();
  const [mode, setMode] = useState<InputMode>("text");
  const [text, setText] = useState("");
  const [file, setFile] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [result, setResult] = useState<EmbeddedAnalyzerExecutionControlView | null>(null);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => client.executeEmbeddedAnalyzer(runID, {
      version: "embedded_analyzer_operator_request.v1",
      text: mode === "text" ? text : undefined,
      file: mode === "file" ? file.trim() : undefined,
      media_type: "text/plain",
      confirmation: "RUN-EMBEDDED-ANALYZER",
    }),
    onSuccess: (value) => {
      setResult(value);
      setConfirmed(false);
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "artifacts"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "events"] });
      void queryClient.invalidateQueries({ queryKey: ["run", runID, "activity"] });
    },
  });
  const hasInput = mode === "text" ? text.length > 0 && new TextEncoder().encode(text).length <= 65_536
    : file.trim().length > 0;

  return <div className="projection-stack embedded-analyzer-panel">
    <section className="detail-section">
      <header className="panel-heading-row">
        <div>
          <h2><Binary aria-hidden="true" size={18} />{t("内置分析器", "Embedded analyzer")}</h2>
          <p>{t("固定 Rust/WASI 摘要分析器，只接收有界输入，不具备文件系统、网络或子进程权限。", "A fixed Rust/WASI summary analyzer accepting only bounded input, with no filesystem, network, or subprocess authority.")}</p>
        </div>
        <span className="safety-chip"><ShieldCheck aria-hidden="true" size={14} />{t("固定隔离边界", "Fixed isolation boundary")}</span>
      </header>
      <div className="prayu-segmented" role="group" aria-label={t("分析器输入方式", "Analyzer input mode")}>
        <button aria-pressed={mode === "text"} onClick={() => setMode("text")}
          type="button"><FileText aria-hidden="true" size={14} />{t("文本", "Text")}</button>
        <button aria-pressed={mode === "file"} onClick={() => setMode("file")}
          type="button"><FileText aria-hidden="true" size={14} />{t("工作区文件", "Workspace file")}</button>
      </div>
      {mode === "text" ? <label className="analyzer-input-field">
        <span>{t("待分析文本", "Text to analyze")}</span>
        <textarea maxLength={65_536} onChange={(event) => setText(event.target.value)}
          placeholder={t("输入不超过 64 KiB 的文本", "Enter up to 64 KiB of text")} rows={8} value={text} />
        <small>{new TextEncoder().encode(text).length} / 65,536 {t("字节", "bytes")}</small>
      </label> : <label className="analyzer-input-field">
        <span>{t("工作区相对路径", "Workspace-relative path")}</span>
        <input onChange={(event) => setFile(event.target.value)}
          placeholder={t("例如 attachments/sample.txt", "For example, attachments/sample.txt")} value={file} />
        <small>{t("仅允许当前 Run 工作区内的普通文件；符号链接逃逸会被 Go 拒绝。", "Only regular files inside the current Run workspace are allowed; Go rejects symlink escapes.")}</small>
      </label>}
      <label className="analyzer-confirmation">
        <input checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)}
          type="checkbox" />
        <span>{t("确认执行固定分析器并把元数据结果写入当前 Run 的 Artifact。", "Confirm running the fixed analyzer and recording its metadata result as an Artifact on this Run.")}</span>
      </label>
      <button className="primary-button" disabled={!confirmed || !hasInput || mutation.isPending}
        onClick={() => mutation.mutate()} type="button">
        {mutation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={15} /> :
          <Binary aria-hidden="true" size={15} />}
        {mutation.isPending ? t("正在分析", "Analyzing") : t("执行分析", "Run analysis")}
      </button>
      {mutation.isError && <ErrorState error={mutation.error} />}
    </section>
    {result && <section className="detail-section analyzer-result" aria-live="polite">
      <h2>{t("分析结果", "Analysis result")}</h2>
      <dl className="settings-row-list">
        <div><dt>{t("状态", "Status")}</dt><dd>{result.status === "succeeded"
          ? t("成功", "Succeeded") : result.status}</dd></div>
        <div><dt>{t("输入字节", "Input bytes")}</dt><dd>{result.input_bytes}</dd></div>
        <div><dt>{t("行数", "Line count")}</dt><dd>{result.line_count}</dd></div>
        <div><dt>SHA-256</dt><dd><code>{result.sha256}</code></dd></div>
        <div><dt>{t("产物", "Artifact")}</dt><dd><code>{result.artifact_id}</code></dd></div>
        <div><dt>{t("隔离", "Isolation")}</dt><dd>{t("无文件系统 / 无网络 / 无子进程", "No filesystem / no network / no subprocesses")}</dd></div>
      </dl>
    </section>}
  </div>;
}
