import type { HostCommandProposalView } from "../api/types";
import { useLocale } from "../lib/locale";

export function SavedHostCommandOutput({ detail }: { detail: HostCommandProposalView }) {
  const { t } = useLocale();
  const output = detail.saved_output;
  const evidence = detail.untrusted_evidence;
  if (output && (output.result_id !== detail.result?.id || output.request_id !== detail.receipt?.request_id)) {
    return <p role="alert">{t("保存输出与当前命令收据不匹配。", "Saved output does not match this command receipt.")}</p>;
  }
  const damaged = output
    ? output.stdout.text.includes("\uFFFD") || output.stderr.text.includes("\uFFFD")
    : evidence?.includes("\uFFFD");
  return <div className="saved-host-command-output">
    {output ? <>
      <p className="saved-host-output-caption">{t("已保存的命令输出，仅作为参考内容，不重新运行命令。",
        "Saved command output is reference data; reading it does not rerun the command.")}</p>
      {(["stdout", "stderr"] as const).map((stream) => {
        const saved = output[stream];
        const label = stream === "stdout" ? t("标准输出", "stdout") : t("标准错误", "stderr");
        return <section aria-label={label} className="saved-host-output-stream" key={stream}>
          <header><strong>{label}</strong><small>{saved.utf8_bytes} B</small></header>
          {saved.text ? <pre>{saved.text}</pre> : <p>{t("无已保存内容", "No saved content")}</p>}
          {saved.truncated && <p className="saved-host-output-warning">{t("此输出已截断，未保存部分无法读取。",
            "This output was truncated; uncaptured content is unavailable.")}</p>}
          {saved.redacted && <small>{t("已进行脱敏处理", "Redaction applied")}</small>}
        </section>;
      })}
    </> : <p>{t("此记录没有单独保存标准输出和标准错误，可展开原始记录查看。",
      "This record did not save stdout and stderr separately. Expand the original record to inspect it.")}</p>}
    {damaged && <p className="saved-host-output-warning">{t("部分输出字符无法正确解码，当前文本可能不完整。",
      "Some characters could not be decoded correctly; the displayed text may be incomplete.")}</p>}
    {evidence ? <details className="saved-host-output-evidence">
      <summary>{t("原始记录与技术信息", "Original record and technical details")}</summary>
      <pre>{evidence}</pre>
    </details> : !output && <p>{t("此历史记录没有可读取的已保存输出。",
      "No saved output is available for this historical record.")}</p>}
  </div>;
}
