import { useRef, useState } from "react";
import "./identity-disclosure.css";

interface IdentityDisclosureProps {
  identity: string;
  identityLabel: string;
  summary: string;
}

function IdentityDisclosure({ identity, identityLabel, summary }: IdentityDisclosureProps) {
  const [expanded, setExpanded] = useState(false);
  const [copyState, setCopyState] = useState<"idle" | "pending" | "copied" | "failed">("idle");
  const copyPending = useRef(false);

  const copy = async () => {
    if (!expanded || copyPending.current) return;
    copyPending.current = true;
    setCopyState("pending");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("Clipboard unavailable");
      await navigator.clipboard.writeText(identity);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    } finally {
      copyPending.current = false;
    }
  };

  return <details className="v2-identity-disclosure" onToggle={(event) => setExpanded(event.currentTarget.open)}>
    <summary tabIndex={0}>{summary}</summary>
    <div className="v2-identity-disclosure-content">
      <dl><dt>{identityLabel}</dt><dd><code>{identity}</code></dd></dl>
      <button aria-disabled={copyState === "pending"} disabled={!expanded} onClick={() => void copy()} type="button">
        {copyState === "pending" ? "正在复制…" : "复制完整 ID"}
      </button>
      <p aria-live="polite" aria-atomic="true" className="v2-identity-copy-status" role="status">
        {copyState === "copied" ? `已复制此${identityLabel}。` : copyState === "failed"
          ? "复制失败，请选中上方完整 ID 手动复制。" : ""}
      </p>
    </div>
  </details>;
}

export function V2IdentityDisclosure(props: IdentityDisclosureProps) {
  // Reset the disclosure and copy result on identity changes. A pending copy
  // belongs to the previous instance and cannot update the new record's status.
  return <IdentityDisclosure key={props.identity} {...props} />;
}
