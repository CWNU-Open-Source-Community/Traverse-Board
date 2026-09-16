export interface ReviewDiffLine { text: string; oldLine?: number; newLine?: number; kind: "context" | "add" | "remove" | "meta" }
export interface ReviewDiffHunk { header: string; lines: ReviewDiffLine[]; oldPath?: string; newPath?: string; preamble: string }

export function parseReviewDiff(patch: string): { preamble: string; hunks: ReviewDiffHunk[]; trailing: string } {
  let outside: string[] = [];
  const hunks: ReviewDiffHunk[] = [];
  let hunk: ReviewDiffHunk | undefined, oldLine = 0, newLine = 0, oldRemaining = 0, newRemaining = 0;
  let oldPath: string | undefined, newPath: string | undefined;
  for (const text of patch.replace(/\r\n/gu, "\n").split("\n")) {
    // Inside the declared ranges, ---/+++ are ordinary removed/added source lines.
    if (hunk && (oldRemaining > 0 || newRemaining > 0)) {
      if (text.startsWith("-") && oldRemaining > 0) {
        hunk.lines.push({ text, kind: "remove", oldLine: oldLine++ }); oldRemaining--; continue;
      }
      if (text.startsWith("+") && newRemaining > 0) {
        hunk.lines.push({ text, kind: "add", newLine: newLine++ }); newRemaining--; continue;
      }
      if (text.startsWith(" ") && oldRemaining > 0 && newRemaining > 0) {
        hunk.lines.push({ text, kind: "context", oldLine: oldLine++, newLine: newLine++ }); oldRemaining--; newRemaining--; continue;
      }
    }
    if (hunk && outside.length === 0 && text.startsWith("\\ No newline at end of file")) {
      hunk.lines.push({ text, kind: "meta" }); continue;
    }
    // Completed or interrupted hunks cannot lend line numbers to later file metadata.
    oldRemaining = 0; newRemaining = 0;
    const match = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?:.*)$/u.exec(text);
    if (match) {
      const oldStart = Number(match[1]), oldCount = Number(match[2] ?? 1);
      const newStart = Number(match[3]), newCount = Number(match[4] ?? 1);
      if ([oldStart, oldCount, newStart, newCount, oldStart + oldCount, newStart + newCount].every(Number.isSafeInteger) &&
        (oldStart > 0 || oldCount === 0) && (newStart > 0 || newCount === 0)) {
        oldLine = oldStart; newLine = newStart; oldRemaining = oldCount; newRemaining = newCount;
        hunk = { header: text, lines: [], oldPath, newPath, preamble: outside.join("\n") };
        outside = []; hunks.push(hunk); continue;
      }
    }
    if (text.startsWith("diff ")) { oldPath = undefined; newPath = undefined; }
    else if (text.startsWith("--- ")) { oldPath = text.slice(4) || undefined; newPath = undefined; }
    else if (text.startsWith("+++ ")) {
      if (!outside.at(-1)?.startsWith("--- ")) oldPath = undefined;
      newPath = text.slice(4) || undefined;
    }
    outside.push(text);
  }
  return { preamble: hunks[0]?.preamble ?? outside.join("\n"), hunks, trailing: hunks.length ? outside.join("\n") : "" };
}

function fileContext(hunk: ReviewDiffHunk): string {
  return `旧文件路径：${hunk.oldPath ?? "未提供"}\n新文件路径：${hunk.newPath ?? "未提供"}`;
}

export function ReviewDiff({ patch, source, onFeedback }: {
  patch: string; source: string; onFeedback?: (context: string) => void;
}) {
  const parsed = parseReviewDiff(patch);
  if (!parsed.hunks.length) return <pre className="v2-delivery-patch">{patch}</pre>;
  return <div className="v2-delivery-diff">
    {parsed.hunks.map((hunk, ordinal) => <section key={ordinal} aria-label={`差异片段 ${ordinal + 1}`}>
      {hunk.preamble && <pre>{hunk.preamble}</pre>}
      <header><code>{hunk.header}</code>{onFeedback && <button type="button" onClick={() => onFeedback(
        `${source}\n${fileContext(hunk)}\n差异片段：${hunk.header}\n${hunk.lines.slice(0, 60).map((line) => line.text).join("\n")}${hunk.lines.length > 60 ? "\n（摘录前60行）" : ""}`)}>引用此片段</button>}</header>
      <div className="v2-delivery-diff-lines">{hunk.lines.map((line, index) => <div className={`v2-diff-line ${line.kind}`} key={index}>
        {onFeedback && line.kind !== "meta" ? <button type="button" className="v2-diff-line-ref"
          aria-label={`引用${line.kind === "remove" ? "旧" : "新"}第 ${line.newLine ?? line.oldLine} 行`}
          onClick={() => onFeedback(`${source}\n${fileContext(hunk)}\n差异片段：${hunk.header}\n${line.kind === "remove" ? "旧文件" : "新文件"}第 ${line.newLine ?? line.oldLine} 行：\n${line.text}\n行号只对应上述差异版本，请先核对当前内容。`)}>
          <span>{line.oldLine ?? ""}</span><span>{line.newLine ?? ""}</span></button> : <span className="v2-diff-line-ref" />}
        <code>{line.text || " "}</code>
      </div>)}</div>
    </section>)}
    {parsed.trailing && <pre>{parsed.trailing}</pre>}
  </div>;
}
