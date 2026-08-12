import { useLayoutEffect, useRef, type RefObject } from "react";

const focusableSelector = [
  "a[href]",
  "area[href]",
  "button:not([disabled])",
  "input:not([disabled]):not([type='hidden'])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[contenteditable='true']",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

function focusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(focusableSelector)).filter((element) =>
    !element.matches(":disabled") &&
    !element.closest("[hidden], [inert], [aria-hidden='true']"));
}

export function useModalFocusTrap<T extends HTMLElement>(open: boolean, onEscape: () => void,
  escapeDisabled = false, initialFocusRef?: RefObject<HTMLElement | null>) {
  const dialogRef = useRef<T>(null);
  const onEscapeRef = useRef(onEscape);
  const escapeDisabledRef = useRef(escapeDisabled);
  onEscapeRef.current = onEscape;
  escapeDisabledRef.current = escapeDisabled;

  useLayoutEffect(() => {
    if (!open || !dialogRef.current) return;
    const dialog = dialogRef.current;
    const previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement : null;
    const candidates = focusableElements(dialog);
    const preferred = initialFocusRef?.current && dialog.contains(initialFocusRef.current)
      ? initialFocusRef.current
      : candidates.find((element) => element.hasAttribute("autofocus")) ?? candidates[0];
    if (!dialog.contains(document.activeElement)) {
      (preferred ?? dialog).focus();
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !escapeDisabledRef.current) {
        event.preventDefault();
        event.stopPropagation();
        onEscapeRef.current();
        return;
      }
      if (event.key !== "Tab" || event.altKey || event.ctrlKey || event.metaKey) return;
      const currentCandidates = focusableElements(dialog);
      if (currentCandidates.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = currentCandidates[0];
      const last = currentCandidates.at(-1)!;
      const active = document.activeElement;
      if (event.shiftKey && (active === first || !dialog.contains(active))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (active === last || !dialog.contains(active))) {
        event.preventDefault();
        first.focus();
      }
    };
    dialog.addEventListener("keydown", handleKeyDown);
    return () => {
      dialog.removeEventListener("keydown", handleKeyDown);
      if (previouslyFocused?.isConnected) previouslyFocused.focus();
    };
  }, [open]);

  return dialogRef;
}
