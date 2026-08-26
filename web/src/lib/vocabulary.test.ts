import { canonicalVocabulary, compatibilityIdentifiers, diagnosticVocabulary } from "./vocabulary";

describe("canonical presentation vocabulary", () => {
  it("keeps the bilingual user and diagnostic terms stable", () => {
    expect([
      ...Object.values(canonicalVocabulary).flat(),
      ...Object.values(diagnosticVocabulary).flat(),
    ]).toMatchInlineSnapshot(`
      [
        "Thread（任务）",
        "Thread",
        "Run（执行尝试）",
        "Run",
        "步骤",
        "Step",
        "工具项",
        "Tool Item",
        "工作区",
        "Workspace",
        "计划项",
        "Plan item",
        "Agent（执行角色）",
        "Agent",
        "Mission（不可变意图与 Scope）",
        "Mission (immutable intent and Scope)",
        "Run 内 Session",
        "Run-local Session",
      ]
    `);
  });

  it("records the durable identifiers that presentation copy must not rename", () => {
    expect(compatibilityIdentifiers).toEqual([
      "cyberagent",
      "cyberagent-workbench",
      "CYBERAGENT_*",
      ".prayu/...",
      "/api/v1/runs",
      "/api/v1/sessions",
      "work_item",
    ]);
  });
});
