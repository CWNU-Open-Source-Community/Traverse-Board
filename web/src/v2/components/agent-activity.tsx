import { CircleAlert, CircleDot } from "lucide-react";
import type { AgentActivity } from "../projection/agent-activity";
import { AgentOrb } from "./agent-orb";
import "./agent-activity.css";

export function V2AgentActivity({ activity }: { activity: AgentActivity | null }) {
  if (!activity) return null;
  return <div className={`v2-agent-activity${activity.attention ? " needs-attention" : ""}`}
    role="status" aria-live="polite" aria-atomic="true">
    {activity.mode ? <AgentOrb mode={activity.mode} />
      : activity.attention ? <CircleAlert size={16} aria-hidden="true" />
        : <CircleDot size={16} aria-hidden="true" />}
    <span>{activity.label}</span>
  </div>;
}
