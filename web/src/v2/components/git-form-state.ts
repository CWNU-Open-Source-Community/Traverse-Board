import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { ThreadGitPreview, ThreadGitState } from "../../api/task-delivery";

export interface GitFormState {
  operation: "commit" | "stage" | "unstage" | "create_branch" | "switch_branch" | "worktree_create" | "push_branch";
  paths: string[];
  message: string;
  branch: string;
  worktreeName: string;
  remote: string;
  credentialName: string;
  basis: string;
  previewed: boolean;
}

type RepositoryContext = Pick<ThreadGitState, "thread_id" | "workspace_id" | "source_workspace_id" | "repository_root">;
export const gitFormRepositoryID = (value: RepositoryContext) => JSON.stringify([
  value.thread_id, value.workspace_id, value.source_workspace_id, value.repository_root,
]);

// The server's binding includes HEAD, index, worktree, status and sequence
// hashes. Include the live Run/Session and advertised remotes as well.
export const gitFormRevision = (value: ThreadGitState | ThreadGitPreview) => JSON.stringify([
  gitFormRepositoryID(value), value.run_id, value.session_id, value.head_oid, value.branch,
  value.binding_fingerprint, [...value.remotes].sort((a, b) => a.name.localeCompare(b.name) || a.url.localeCompare(b.url)),
]);

// The existing three text fields predate repository binding. Adopt them only
// for this page's first actual repository for a task, never for another path.
const legacyRepositories = new WeakMap<QueryClient, Map<string, string>>();

export function useGitFormState(baseURL: string, threadID: string, repository: ThreadGitState | undefined,
  legacy: Pick<GitFormState, "message" | "branch" | "worktreeName">) {
  const client = useQueryClient();
  const repositoryID = repository ? gitFormRepositoryID(repository) : "";
  const queryKey = ["thread", threadID, "git-form", baseURL, repositoryID] as const;
  const query = useQuery<GitFormState>({ queryKey, enabled: false, gcTime: Infinity,
    queryFn: () => { throw new Error("Git 表单无需网络读取。"); },
    initialData: () => {
      let preserved = { message: "", branch: "", worktreeName: "" };
      if (repositoryID) {
        const bindings = legacyRepositories.get(client) ?? new Map<string, string>();
        legacyRepositories.set(client, bindings);
        const task = JSON.stringify([baseURL, threadID]);
        if (!bindings.has(task)) bindings.set(task, repositoryID);
        if (bindings.get(task) === repositoryID) preserved = {
          message: typeof legacy.message === "string" ? legacy.message : "",
          branch: typeof legacy.branch === "string" ? legacy.branch : "",
          worktreeName: typeof legacy.worktreeName === "string" ? legacy.worktreeName : "",
        };
      }
      return { operation: "commit", paths: [], ...preserved, remote: "", credentialName: "",
        basis: repository ? gitFormRevision(repository) : "", previewed: false };
    },
  });
  const update = (change: Partial<GitFormState> | ((previous: GitFormState) => GitFormState)) => {
    client.setQueryData<GitFormState>(queryKey, (previous) => {
      const current = previous ?? query.data;
      return typeof change === "function" ? change(current) : { ...current, ...change };
    });
  };
  return { value: query.data, update, repositoryID };
}
