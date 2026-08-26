import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderPlus, LoaderCircle, Plus, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  ThreadCreationControlRequestView,
  ThreadCreationControlView,
  WorkspaceView,
} from "../api/types";
import { useConnectionStore } from "../state/connection";
import { useLocale } from "../lib/locale";
import {
  desktopWorkspaceImportEnabled,
  importDesktopWorkspace,
} from "../lib/desktop-bridge";
import { useModalFocusTrap } from "../hooks/use-modal-focus-trap";

const profiles: Array<NonNullable<ThreadCreationControlRequestView["profile"]>> = ["code", "review", "learn", "script"];
const surfaces: Array<NonNullable<ThreadCreationControlRequestView["surface"]>> = ["code", "cyber"];

interface RetryIntent {
  fingerprint: string;
  key: string;
}

export function RunCreationDialog({ client, open, onClose, initialGoal = "",
  initialPhase = "deliver" }: {
  client: CyberAgentClient;
  open: boolean;
  onClose: () => void;
  initialGoal?: string;
  initialPhase?: NonNullable<ThreadCreationControlRequestView["phase"]>;
}) {
  const { t } = useLocale();
  const [goal, setGoal] = useState("");
  const [workspaceID, setWorkspaceID] = useState("");
  const [workspaceName, setWorkspaceName] = useState("");
  const [profile, setProfile] = useState<NonNullable<ThreadCreationControlRequestView["profile"]>>("code");
  const [surface, setSurface] = useState<NonNullable<ThreadCreationControlRequestView["surface"]>>("code");
  const [phase, setPhase] = useState<NonNullable<ThreadCreationControlRequestView["phase"]>>("deliver");
  const retryIntent = useRef<RetryIntent | null>(null);
  const wasOpen = useRef(false);
  const queryClient = useQueryClient();
  const selectThread = useConnectionStore((state) => state.selectThread);
  const nativeWorkspaceImport = desktopWorkspaceImportEnabled();
  const workspaces = useQuery({
    queryKey: ["workspaces"],
    queryFn: ({ signal }) => client.getPage<WorkspaceView>("/workspaces", { limit: 100 }, "", signal),
    enabled: open && !nativeWorkspaceImport,
    staleTime: 30_000,
  });
  const workspaceImport = useMutation({
    mutationFn: importDesktopWorkspace,
    onSuccess: (workspace) => {
      if (workspace) {
        setWorkspaceID(workspace.id);
        setWorkspaceName(workspace.name);
        mutation.reset();
        void queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      }
    },
  });
  const mutation = useMutation({
    mutationFn: ({ request, key }: { request: ThreadCreationControlRequestView; key: string }) =>
      client.createThread(request, key),
    onSuccess: (result: ThreadCreationControlView) => {
      retryIntent.current = null;
      setGoal("");
      void queryClient.invalidateQueries({ queryKey: ["runs"] });
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      void queryClient.invalidateQueries({ queryKey: ["threads"] });
      selectThread(result.thread.id);
      onClose();
    },
  });
  const resetMutation = mutation.reset;
  const resetWorkspaceImport = workspaceImport.reset;
  const startWorkspaceImport = workspaceImport.mutate;

  useEffect(() => {
    if (open && !wasOpen.current) {
      setGoal(initialGoal);
      setPhase(initialPhase);
      retryIntent.current = null;
      resetMutation();
      resetWorkspaceImport();
      if (nativeWorkspaceImport) {
        setWorkspaceID("");
        setWorkspaceName("");
        startWorkspaceImport();
      }
    }
    wasOpen.current = open;
  }, [initialGoal, initialPhase, nativeWorkspaceImport, open, resetMutation,
    resetWorkspaceImport, startWorkspaceImport]);

  useEffect(() => {
    if (!nativeWorkspaceImport && !workspaceID && workspaces.data?.items[0]) {
      setWorkspaceID(workspaces.data.items[0].id);
    }
  }, [nativeWorkspaceImport, workspaceID, workspaces.data]);
  const busy = mutation.isPending || workspaceImport.isPending;
  const dialogRef = useModalFocusTrap<HTMLFormElement>(open, onClose, busy);

  if (!open) {
    return null;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const request: ThreadCreationControlRequestView = {
      version: "thread_creation.v1",
      goal: goal.trim(),
      workspace_id: workspaceID,
      profile,
      surface,
      phase,
    };
    const fingerprint = JSON.stringify(request);
    if (retryIntent.current?.fingerprint !== fingerprint) {
      retryIntent.current = {
        fingerprint,
        key: `web-run-create-${globalThis.crypto.randomUUID()}`,
      };
    }
    mutation.mutate({ request, key: retryIntent.current.key });
  };

  const close = () => {
    if (!busy) {
      onClose();
    }
  };
  const options = workspaces.data?.items ?? [];
  const goalBytes = new TextEncoder().encode(goal.trim()).byteLength;
  const goalTooLarge = goalBytes > 4096;
  const ready = goalBytes > 0 && !goalTooLarge && workspaceID !== "" && !busy;

  return (
    <div className="desktop-dialog-backdrop" role="presentation">
      <form aria-labelledby="run-creation-title" aria-modal="true" className="desktop-dialog run-creation-dialog"
        onSubmit={submit} ref={dialogRef} role="dialog" tabIndex={-1}>
        <header>
          <div>
            <span className="dialog-icon"><Plus aria-hidden="true" size={17} /></span>
            <div><h2 id="run-creation-title">{t("新建 Thread（任务）", "New Thread")}</h2><small>{t("稳定任务与历史", "Stable task and history")} · {t("针路簿", "Traverse Board")}</small></div>
          </div>
          <button aria-label={t("关闭", "Close")} className="icon-button" disabled={busy}
            onClick={close} title={t("关闭", "Close")} type="button"><X aria-hidden="true" size={16} /></button>
        </header>
        <div className="desktop-dialog-body run-creation-form">
          {nativeWorkspaceImport ? <div className="workspace-import-field">
            <span>{t("工作区", "Workspace")}</span>
            <button aria-label={t("选择工作文件夹", "Select working folder")}
              className={workspaceID ? "workspace-import-control selected" : "workspace-import-control"}
              disabled={busy} onClick={() => {
                workspaceImport.reset();
                mutation.reset();
                workspaceImport.mutate();
              }} type="button">
              {workspaceImport.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={18} /> :
                <FolderPlus aria-hidden="true" size={18} />}
              <span><strong>{workspaceName || t("选择目录", "Choose folder")}</strong>
                <small>{workspaceID ? t("已注册为此 Thread 的工作区", "Registered for this Thread") :
                  t("选择针路簿可读取和编辑的目录", "Choose a directory Traverse Board may read and edit")}</small></span>
            </button>
          </div> : <label><span>{t("工作区", "Workspace")}</span>
            <select disabled={workspaces.isLoading || options.length === 0} onChange={(event) => {
              setWorkspaceID(event.target.value);
              mutation.reset();
            }} value={workspaceID}>
              {options.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}
            </select>
          </label>}
          <label><span>{t("目标", "Goal")}</span>
            <textarea autoFocus maxLength={4096} onChange={(event) => {
              setGoal(event.target.value);
              mutation.reset();
            }} rows={5} value={goal} />
          </label>
          <label><span>{t("执行档位", "Profile")}</span>
            <select onChange={(event) => {
              setProfile(event.target.value as typeof profile);
              mutation.reset();
            }} value={profile}>
              {profiles.map((value) => <option key={value} value={value}>{t(
                value === "code" ? "编程" : value === "review" ? "审查" : value === "learn" ? "学习" : "脚本",
                value,
              )}</option>)}
            </select>
          </label>
          <div className="run-creation-choice-row single">
            <fieldset><legend>{t("工作模式", "Surface")}</legend><div className="run-creation-segments">
              {surfaces.map((value) => <button aria-pressed={surface === value} className={surface === value ? "selected" : ""}
                key={value} onClick={() => { setSurface(value); mutation.reset(); }} type="button">{value}</button>)}
            </div></fieldset>
          </div>
          {!nativeWorkspaceImport && workspaces.isError && <p className="connection-error">{t("工作区列表不可用", "Workspace list unavailable")}</p>}
          {!nativeWorkspaceImport && !workspaces.isLoading && options.length === 0 && <p className="connection-error">{t("尚未注册工作区", "No Workspace registered")}</p>}
          {workspaceImport.isError && <p className="connection-error">{errorMessage(workspaceImport.error)}</p>}
          {goalTooLarge && <p className="connection-error">{t("目标超过 4096 个 UTF-8 字节", "Goal exceeds 4096 UTF-8 bytes")}</p>}
          {mutation.isError && <p className="connection-error">{errorMessage(mutation.error)}</p>}
        </div>
        <footer className="run-creation-actions">
          <button className="dialog-secondary" disabled={busy} onClick={close} type="button">{t("取消", "Cancel")}</button>
          <button className="dialog-primary" disabled={!ready} type="submit">
            {mutation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={16} /> : <Plus aria-hidden="true" size={16} />}
            {t("创建 Thread", "Create Thread")}
          </button>
        </footer>
      </form>
    </div>
  );
}

function errorMessage(value: unknown): string {
  return value instanceof Error && value.message.trim() ? value.message : "Thread creation failed";
}
