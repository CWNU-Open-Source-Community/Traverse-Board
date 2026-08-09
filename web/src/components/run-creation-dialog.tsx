import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, Plus, X } from "lucide-react";
import type { CyberAgentClient } from "../api/client";
import type {
  RunCreationControlRequestView,
  RunCreationControlView,
  WorkspaceView,
} from "../api/types";
import { useConnectionStore } from "../state/connection";
import { useLocale } from "../lib/locale";

const profiles: Array<NonNullable<RunCreationControlRequestView["profile"]>> = ["code", "review", "learn", "script"];
const surfaces: Array<NonNullable<RunCreationControlRequestView["surface"]>> = ["code", "cyber"];

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
  initialPhase?: NonNullable<RunCreationControlRequestView["phase"]>;
}) {
  const { t } = useLocale();
  const [goal, setGoal] = useState("");
  const [workspaceID, setWorkspaceID] = useState("");
  const [profile, setProfile] = useState<NonNullable<RunCreationControlRequestView["profile"]>>("code");
  const [surface, setSurface] = useState<NonNullable<RunCreationControlRequestView["surface"]>>("code");
  const [phase, setPhase] = useState<NonNullable<RunCreationControlRequestView["phase"]>>("deliver");
  const retryIntent = useRef<RetryIntent | null>(null);
  const wasOpen = useRef(false);
  const queryClient = useQueryClient();
  const selectRun = useConnectionStore((state) => state.selectRun);
  const workspaces = useQuery({
    queryKey: ["workspaces"],
    queryFn: ({ signal }) => client.getPage<WorkspaceView>("/workspaces", { limit: 100 }, "", signal),
    enabled: open,
    staleTime: 30_000,
  });
  const mutation = useMutation({
    mutationFn: ({ request, key }: { request: RunCreationControlRequestView; key: string }) =>
      client.createRun(request, key),
    onSuccess: (result: RunCreationControlView) => {
      retryIntent.current = null;
      setGoal("");
      void queryClient.invalidateQueries({ queryKey: ["runs"] });
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      selectRun(result.run.id);
      onClose();
    },
  });
  const resetMutation = mutation.reset;

  useEffect(() => {
    if (open && !wasOpen.current) {
      setGoal(initialGoal);
      setPhase(initialPhase);
      retryIntent.current = null;
      resetMutation();
    }
    wasOpen.current = open;
  }, [initialGoal, initialPhase, open, resetMutation]);

  useEffect(() => {
    if (!workspaceID && workspaces.data?.items[0]) {
      setWorkspaceID(workspaces.data.items[0].id);
    }
  }, [workspaceID, workspaces.data]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !mutation.isPending) {
        onClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [mutation.isPending, onClose, open]);

  if (!open) {
    return null;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const request: RunCreationControlRequestView = {
      version: "run_creation.v1",
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
    if (!mutation.isPending) {
      onClose();
    }
  };
  const options = workspaces.data?.items ?? [];
  const goalBytes = new TextEncoder().encode(goal.trim()).byteLength;
  const goalTooLarge = goalBytes > 4096;
  const ready = goalBytes > 0 && !goalTooLarge && workspaceID !== "" && !mutation.isPending;

  return (
    <div className="desktop-dialog-backdrop" role="presentation">
      <form aria-labelledby="run-creation-title" aria-modal="true" className="desktop-dialog run-creation-dialog"
        onSubmit={submit} role="dialog">
        <header>
          <div>
            <span className="dialog-icon"><Plus aria-hidden="true" size={17} /></span>
            <div><h2 id="run-creation-title">{t("新建 Run", "New Run")}</h2><small>Prayu</small></div>
          </div>
          <button aria-label={t("关闭", "Close")} className="icon-button" disabled={mutation.isPending}
            onClick={close} title={t("关闭", "Close")} type="button"><X aria-hidden="true" size={16} /></button>
        </header>
        <div className="desktop-dialog-body run-creation-form">
          <label><span>{t("工作区", "Workspace")}</span>
            <select disabled={workspaces.isLoading || options.length === 0} onChange={(event) => {
              setWorkspaceID(event.target.value);
              mutation.reset();
            }} value={workspaceID}>
              {options.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}
            </select>
          </label>
          <label><span>{t("目标", "Goal")}</span>
            <textarea autoFocus maxLength={4096} onChange={(event) => {
              setGoal(event.target.value);
              mutation.reset();
            }} rows={5} value={goal} />
          </label>
          <label><span>{t("任务类型", "Profile")}</span>
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
          {workspaces.isError && <p className="connection-error">{t("工作区列表不可用", "Workspace list unavailable")}</p>}
          {!workspaces.isLoading && options.length === 0 && <p className="connection-error">{t("尚未注册工作区", "No Workspace registered")}</p>}
          {goalTooLarge && <p className="connection-error">{t("目标超过 4096 个 UTF-8 字节", "Goal exceeds 4096 UTF-8 bytes")}</p>}
          {mutation.isError && <p className="connection-error">{errorMessage(mutation.error)}</p>}
        </div>
        <footer className="run-creation-actions">
          <button className="dialog-secondary" disabled={mutation.isPending} onClick={close} type="button">{t("取消", "Cancel")}</button>
          <button className="dialog-primary" disabled={!ready} type="submit">
            {mutation.isPending ? <LoaderCircle aria-hidden="true" className="spin" size={16} /> : <Plus aria-hidden="true" size={16} />}
            {t("创建 Run", "Create Run")}
          </button>
        </footer>
      </form>
    </div>
  );
}

function errorMessage(value: unknown): string {
  return value instanceof Error && value.message.trim() ? value.message : "Run creation failed";
}
