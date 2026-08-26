export const canonicalVocabulary = {
  thread: ["Thread（任务）", "Thread"],
  run: ["Run（执行尝试）", "Run"],
  step: ["步骤", "Step"],
  toolItem: ["工具项", "Tool Item"],
  workspace: ["工作区", "Workspace"],
  planItem: ["计划项", "Plan item"],
  agent: ["Agent（执行角色）", "Agent"],
} as const;

export const diagnosticVocabulary = {
  mission: ["Mission（不可变意图与 Scope）", "Mission (immutable intent and Scope)"],
  session: ["Run 内 Session", "Run-local Session"],
} as const;

export const compatibilityIdentifiers = [
  "cyberagent",
  "cyberagent-workbench",
  "CYBERAGENT_*",
  ".prayu/...",
  "/api/v1/runs",
  "/api/v1/sessions",
  "work_item",
] as const;
