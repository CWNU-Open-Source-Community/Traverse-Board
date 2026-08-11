export type UnifiedDiffLineKind = "add" | "delete" | "context" | "hunk" | "meta";

export type UnifiedDiffLine = {
  kind: UnifiedDiffLineKind;
  marker: string;
  oldLine?: number;
  newLine?: number;
  text: string;
};

export type UnifiedDiff = {
  additions: number;
  deletions: number;
  lines: UnifiedDiffLine[];
};

const hunkPattern = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/;

export function parseUnifiedDiff(value: string): UnifiedDiff {
  let additions = 0;
  let deletions = 0;
  let oldLine: number | undefined;
  let newLine: number | undefined;
  const lines: UnifiedDiffLine[] = [];

  for (const rawLine of value.replace(/\r\n/g, "\n").split("\n")) {
    const hunk = hunkPattern.exec(rawLine);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      lines.push({ kind: "hunk", marker: "@@", text: rawLine });
      continue;
    }
    if (rawLine.startsWith("+++") || rawLine.startsWith("---") ||
      rawLine.startsWith("diff ") || rawLine.startsWith("index ") ||
      rawLine.startsWith("\\ No newline")) {
      lines.push({ kind: "meta", marker: "", text: rawLine });
      continue;
    }
    if (rawLine.startsWith("+")) {
      additions += 1;
      lines.push({ kind: "add", marker: "+", newLine, text: rawLine.slice(1) });
      if (newLine !== undefined) newLine += 1;
      continue;
    }
    if (rawLine.startsWith("-")) {
      deletions += 1;
      lines.push({ kind: "delete", marker: "-", oldLine, text: rawLine.slice(1) });
      if (oldLine !== undefined) oldLine += 1;
      continue;
    }
    if (rawLine.startsWith(" ")) {
      lines.push({ kind: "context", marker: "", oldLine, newLine, text: rawLine.slice(1) });
      if (oldLine !== undefined) oldLine += 1;
      if (newLine !== undefined) newLine += 1;
      continue;
    }
    lines.push({ kind: "meta", marker: "", text: rawLine });
  }

  return { additions, deletions, lines };
}
