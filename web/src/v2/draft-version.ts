import type { DraftRef, DraftScope } from "./draft-document";

// Local journal identity only; the API receives the admitted message body.
export interface V2DraftVersion { scope: DraftScope; ref: DraftRef }

const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);
const exactKeys = (value: Record<string, unknown>, keys: string[]) =>
  Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));
const identity = (value: unknown): value is string => typeof value === "string" && /^[\w.-]{1,256}$/u.test(value);

export function validV2DraftVersion(value: unknown, workspaceID: string, threadID?: string): value is V2DraftVersion {
  if (!identity(workspaceID) || !record(value) || !exactKeys(value, ["scope", "ref"]) ||
    !record(value.scope) || !exactKeys(value.scope, ["key", "workspaceID"]) ||
    value.scope.workspaceID !== workspaceID || !record(value.ref) || !exactKeys(value.ref, ["branchID", "seq"]) ||
    !identity(value.ref.branchID) || !Number.isSafeInteger(value.ref.seq) || Number(value.ref.seq) < 1) return false;
  // A first turn retains the original new-dialogue scope through creation.
  // Existing Thread drafts may only name the exact destination Thread.
  return value.scope.key === `new:${workspaceID}` ||
    (identity(threadID) && value.scope.key === `thread:${threadID}`);
}
