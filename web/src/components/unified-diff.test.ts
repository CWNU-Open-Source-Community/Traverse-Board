import { parseUnifiedDiff } from "./unified-diff";

describe("parseUnifiedDiff", () => {
  it("tracks line numbers and excludes file headers from change counts", () => {
    const result = parseUnifiedDiff([
      "--- a/file.txt",
      "+++ b/file.txt",
      "@@ -4,2 +4,3 @@ section",
      " same",
      "-old",
      "+new",
      "+extra",
    ].join("\n"));

    expect(result.additions).toBe(2);
    expect(result.deletions).toBe(1);
    expect(result.lines.filter((line) => line.kind === "add")).toEqual([
      { kind: "add", marker: "+", newLine: 5, text: "new" },
      { kind: "add", marker: "+", newLine: 6, text: "extra" },
    ]);
    expect(result.lines.find((line) => line.kind === "delete")).toEqual(
      { kind: "delete", marker: "-", oldLine: 5, text: "old" },
    );
  });

  it("still summarizes bounded diffs without hunk metadata", () => {
    const result = parseUnifiedDiff("-before\n+after");
    expect(result).toMatchObject({ additions: 1, deletions: 1 });
    expect(result.lines[0]?.oldLine).toBeUndefined();
  });
});
