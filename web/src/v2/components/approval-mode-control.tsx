import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, ShieldCheck, ShieldOff, UserCheck } from "lucide-react";
import type { ApprovalModeControlProps, ExecutionApprovalMode, FullActivationState } from "./approval-mode-contract";
import { V2ConfirmDialog } from "./dialog";

const choices = [
  { mode: "ask", label: "请求批准", icon: UserCheck,
    detail: "运行影响可核验的常规操作；普通公网请求需批准。" },
  { mode: "auto", label: "帮我批准", icon: ShieldCheck,
    detail: "可自动批准已核验的常规操作和普通公网请求。" },
  { mode: "full", label: "完全访问权限", icon: ShieldOff,
    detail: "减少逐次批准；实际访问仍受系统、供应商和运行环境限制。" },
] as const;

const activationLabels: Record<FullActivationState, string> = {
  inactive: "未激活", active: "已激活", unavailable: "不可用",
};

export function V2ApprovalModeControl({ mode, fullActivation, fullUnavailableReason,
  pending, disabled = false, error, variant = "menu", onRequestChange }: ApprovalModeControlProps) {
  const [open, setOpen] = useState(false);
  const [confirmation, setConfirmation] = useState<{
    mode: ExecutionApprovalMode; activation: FullActivationState;
  } | null>(null);
  const confirmingRef = useRef(false);
  const shellRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const settingsRef = useRef<HTMLElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const focusLastRef = useRef(false);
  const tabbingRef = useRef(false);
  const id = useId();
  const blocked = pending || disabled;
  const selected = choices.find((choice) => choice.mode === mode)!;
  const SelectedIcon = selected.icon;
  const coldFull = mode === "full" && fullActivation === "inactive";
  const unavailable = fullUnavailableReason?.trim() || "当前环境未提供完全访问权限。";
  const status = `完全访问${activationLabels[fullActivation]}`;
  const confirmationOpen = confirmation !== null && confirmation.mode === mode &&
    confirmation.activation === fullActivation && !disabled && fullActivation !== "unavailable";

  // Refreshed host state invalidates an old confirmation, never grants access.
  useEffect(() => {
    if (confirmation) {
      const target = returnFocusRef.current;
      if (!target?.isConnected || (target instanceof HTMLButtonElement && target.disabled)) {
        (variant === "menu" ? triggerRef.current : settingsRef.current)?.focus();
      }
    }
    confirmingRef.current = false;
    setConfirmation(null);
  }, [mode, fullActivation, disabled, variant]);

  useEffect(() => {
    if (!open) return;
    tabbingRef.current = false;
    const items = Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []);
    (items[focusLastRef.current ? items.length - 1 : 0] ?? menuRef.current)?.focus();
    const outside = (event: PointerEvent) => {
      if (!shellRef.current?.contains(event.target as Node)) setOpen(false);
    };
    window.addEventListener("pointerdown", outside);
    return () => window.removeEventListener("pointerdown", outside);
  }, [open]);

  const closeMenu = (restoreFocus = false) => {
    setOpen(false);
    if (restoreFocus) triggerRef.current?.focus();
  };
  const choose = (next: ExecutionApprovalMode, trigger: HTMLButtonElement) => {
    if (blocked || (next === "full" && fullActivation === "unavailable")) return;
    if (next === mode && !coldFull) return;
    if (next === "full") {
      returnFocusRef.current = variant === "menu" ? triggerRef.current : trigger;
      confirmingRef.current = true;
      setConfirmation({ mode, activation: fullActivation });
      closeMenu();
      return;
    }
    closeMenu(true);
    onRequestChange({ mode: next, confirmFull: false });
  };
  const cancel = () => {
    if (pending) return;
    confirmingRef.current = false;
    setConfirmation(null);
  };
  const confirm = () => {
    if (blocked || !confirmationOpen || !confirmingRef.current) return;
    // Close synchronously before notifying the host so repeated confirmation
    // cannot submit again before pending props arrive.
    confirmingRef.current = false;
    setConfirmation(null);
    if (variant === "settings") returnFocusRef.current = settingsRef.current;
    onRequestChange({ mode: "full", confirmFull: true });
  };

  const options = <>
    <div aria-label="执行权限档位" className="v2-permission-options" role="group">
      {choices.map(({ mode: value, label, detail, icon: Icon }) => {
        const active = value === mode;
        return <button aria-label={label} aria-describedby={`${id}-${value}${value === "full" && fullActivation === "unavailable" ? ` ${id}-unavailable` : ""}`}
          aria-checked={variant === "menu" ? active : undefined}
          aria-pressed={variant === "settings" ? active : undefined}
          className={value === "full" ? "is-risk" : ""}
          disabled={blocked || active || (value === "full" && fullActivation === "unavailable")}
          key={value} onClick={(event) => choose(value, event.currentTarget)}
          role={variant === "menu" ? "menuitemradio" : undefined}
          tabIndex={variant === "menu" ? -1 : undefined} type="button">
          <Icon aria-hidden="true" size={17} /><span><strong>{label}</strong>
            <small id={`${id}-${value}`}>{value === "full" && fullActivation === "unavailable"
              ? "当前不可用" : value === "full" && active ? `${detail} 已选择 · ${activationLabels[fullActivation]}` : detail}</small></span>
          {active ? <Check aria-hidden="true" size={15} /> : null}
        </button>;
      })}
    </div>
    {coldFull && <button className="v2-permission-downgrade" disabled={blocked}
      onClick={(event) => choose("full", event.currentTarget)}
      role={variant === "menu" ? "menuitem" : undefined}
      tabIndex={variant === "menu" ? -1 : undefined} type="button">
      <ShieldOff aria-hidden="true" size={16} /><span><strong>重新激活完全访问权限</strong>
        <small>已保存选择；重新激活仍需确认。</small></span>
    </button>}
    <small>影响未知、敏感数据外发、破坏性或共享写入操作不会因选择“帮我批准”而自动获准。工具自称只读不代表已通过核验。</small>
  </>;

  return <div aria-busy={pending} className={`v2-permission-control is-${variant}`} ref={shellRef}>
    {variant === "menu" ? <>
      <button aria-controls={open ? id : undefined} aria-expanded={open} aria-haspopup="menu"
        className={`v2-composer-chip${mode === "full" ? " is-risk" : ""}`}
        onClick={() => { focusLastRef.current = false; setOpen((value) => !value); }}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault(); focusLastRef.current = event.key === "ArrowUp"; setOpen(true);
          }
        }} ref={triggerRef} type="button">
        <SelectedIcon aria-hidden="true" size={14} />{selected.label}
        {mode === "full" ? ` · ${activationLabels[fullActivation]}` : ""}
        <ChevronDown aria-hidden="true" size={13} />
      </button>
      {open && <div aria-label="选择执行权限" className="v2-permission-popover" id={id}
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null) &&
            (event.relatedTarget !== triggerRef.current || tabbingRef.current)) setOpen(false);
        }} onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault(); event.stopPropagation(); closeMenu(true); return;
          }
          if (event.key === "Tab") { tabbingRef.current = true; return; }
          if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
          event.preventDefault(); event.stopPropagation();
          const items = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
          const current = items.indexOf(document.activeElement as HTMLButtonElement);
          const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
            : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
          items[next]?.focus();
        }} ref={menuRef} role="menu" tabIndex={-1}>
        <header><strong>执行权限</strong><span>{status}</span></header>{options}
      </div>}
    </> : <section aria-label="执行权限" className="v2-settings-card v2-permission-settings-card"
      ref={settingsRef} tabIndex={-1}>
      <header><div><h2>执行权限</h2><p>{status}</p></div></header>{options}
    </section>}
    {fullActivation === "unavailable" && <p className="v2-inline-error" id={`${id}-unavailable`}>{unavailable}</p>}
    {pending && <span role="status">正在更新权限…</span>}
    {error && <p className="v2-inline-error" role="alert">{error}</p>}
    {confirmationOpen && createPortal(<V2ConfirmDialog open busy={pending}
      confirmLabel={coldFull ? "确认重新激活" : "确认启用"} danger
      description="将请求无需逐次批准的文件、命令和网络访问，可能造成数据丢失或敏感信息泄露。实际可用范围仍受操作系统、供应商和运行环境限制。"
      onCancel={cancel} onConfirm={confirm} returnFocusRef={returnFocusRef}
      title={coldFull ? "重新激活完全访问权限？" : "启用完全访问权限？"} />, document.body)}
  </div>;
}
