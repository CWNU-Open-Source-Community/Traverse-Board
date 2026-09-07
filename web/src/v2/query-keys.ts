export const v2QueryKeys = {
  threads: (status: "active" | "archived" = "active") => ["v2", "threads", status] as const,
  thread: (threadID: string) => ["v2", "thread", threadID] as const,
  transcript: (threadID: string) => ["v2", "thread", threadID, "transcript"] as const,
  permission: (threadID: string) => ["v2", "thread", threadID, "permission"] as const,
  searchReadiness: (threadID: string) => ["v2", "thread", threadID, "search-readiness"] as const,
  approvals: (runID: string) => ["v2", "run", runID, "approvals"] as const,
  workspaces: ["v2", "workspaces"] as const,
};
