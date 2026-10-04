import type { CyberAgentClient } from "../api/client";
import { V2PermissionControl } from "../v2/components/permission-control";

// Both shells use the same three-mode writer and confirmation flow.
export type { ThreadExecutionPermissionControlView, ThreadExecutionPermissionView } from "../api/types";

export function ThreadPermissionSettings({ client, threadID }: {
  client: CyberAgentClient;
  threadID: string;
}) {
  return <V2PermissionControl client={client} threadID={threadID} variant="settings" />;
}
