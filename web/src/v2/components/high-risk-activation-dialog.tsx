import { useId, useRef, type FormEvent, type RefObject } from "react";
import { createPortal } from "react-dom";
import {
  AlertTriangle,
  Bug,
  FolderOpen,
  Globe2,
  ShieldOff,
  SquareTerminal,
} from "lucide-react";
import { useModalFocusTrap } from "../../hooks/use-modal-focus-trap";

export type V2HighRiskProfile = "full_access" | "debug";
export type V2HighRiskActivationPhase = "idle" | "applying" | "confirming" | "restarting";

interface CapabilityCopy {
  title: string;
  description: string;
  icon: typeof FolderOpen;
}

interface ProfileCopy {
  title: string;
  introduction: string;
  capabilities: CapabilityCopy[];
  risk: string;
  boundary: string;
  details: string;
  confirmLabel: string;
}

const profileCopy: Record<V2HighRiskProfile, ProfileCopy> = {
  full_access: {
    title: "要开启完全访问权限吗？",
    introduction: "当前任务将能够在当前系统用户权限范围内，无需逐次批准地运行命令、使用互联网、控制浏览器，并在此设备的任意位置创建和编辑文件。包括但不限于：",
    capabilities: [
      {
        title: "文件和文件夹",
        description: "读取、创建、修改、上传或删除此设备上任意位置的文件",
        icon: FolderOpen,
      },
      {
        title: "终端命令",
        description: "运行未沙箱化的按次命令、安装软件和更改系统设置",
        icon: SquareTerminal,
      },
      {
        title: "互联网、浏览器和已连接的应用",
        description: "访问网站、发送数据并使用已启用的集成；完整 CDP 默认开启，可单独关闭",
        icon: Globe2,
      },
    ],
    risk: "这可能导致敏感数据丢失或泄露，也可能放大提示注入带来的风险。",
    boundary: "确认后仅当前任务切换为完全访问，不会重启应用，也不会更改其他任务的权限。当前执行必须已暂停并处于静止边界，否则系统会提示先暂停。切换到较低档位后新调用立即失效；既有进程由内核在安全边界终止。",
    details: "完全访问包含宿主文件、网络和未沙箱化按次命令。完整 CDP 作为其子开关默认开启，可在权限页单独关闭或经风险确认后重新开启；它不是另一档权限。完全访问不包含持久终端、后台进程或限时终端输入。",
    confirmLabel: "启用完全访问",
  },
  debug: {
    title: "要开启调试模式吗？",
    introduction: "调试模式继承完全访问的全部能力，并允许 Traverse 维持终端和后台进程。仅应在你信任当前工作区及其内容时使用。包括：",
    capabilities: [
      {
        title: "完全访问的全部能力",
        description: "访问任意文件和网络、运行未沙箱化命令；完整 CDP 默认开启，可在权限页关闭",
        icon: ShieldOff,
      },
      {
        title: "持久终端和后台进程",
        description: "在对话执行期间保留终端会话和后台进程",
        icon: SquareTerminal,
      },
      {
        title: "终端输入与调试控制",
        description: "在单独的限时授权内向持久终端输入并执行调试操作",
        icon: Bug,
      },
    ],
    risk: "终端和后台进程可能在界面操作结束后继续运行，凭证、环境变量和调试数据也可能暴露。",
    boundary: "确认后，系统会再次显示原生警告并重启同一个应用，以初始化持久终端、后台进程和限时终端输入运行时。重启不会改写任何任务的已保存权限；任务仍需逐个选择调试模式。",
    details: "这次重启只用于建立长生命周期调试运行时，不是管理员提权。完全访问无需重启，在当前执行暂停并静止后即可开启；降低权限会立即阻断新调用。",
    confirmLabel: "启用并重启",
  },
};

export function V2HighRiskActivationDialog({ open, profile, phase = "idle", error,
  returnFocusRef, onCancel, onConfirm }: {
  open: boolean;
  profile: V2HighRiskProfile | null;
  phase?: V2HighRiskActivationPhase;
  error?: string;
  returnFocusRef?: RefObject<HTMLElement | null>;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const titleID = useId();
  const descriptionID = useId();
  const boundaryID = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);
  const busy = phase !== "idle";
  const dialogRef = useModalFocusTrap<HTMLElement>(open, onCancel, busy, cancelRef, {
    isolateBackground: true,
    returnFocusRef,
  });
  if (!open || !profile) return null;

  const copy = profileCopy[profile];
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!busy) onConfirm();
  };
  const dialog = <div className="v2-overlay v2-high-risk-overlay" role="presentation"
    onMouseDown={(event) => {
      if (event.target === event.currentTarget && !busy) onCancel();
    }}>
    <section aria-busy={busy} aria-describedby={`${descriptionID} ${boundaryID}`}
      aria-labelledby={titleID} aria-modal="true" className="v2-high-risk-dialog"
      ref={dialogRef} role="dialog" tabIndex={-1}>
      <form onSubmit={submit}>
        <div className="v2-high-risk-content">
          <header className="v2-high-risk-heading">
            <AlertTriangle aria-hidden="true" size={22} />
            <h2 id={titleID}>{copy.title}</h2>
          </header>
          <p className="v2-high-risk-introduction" id={descriptionID}>{copy.introduction}</p>
          <div className="v2-high-risk-capabilities">
            {copy.capabilities.map(({ title, description, icon: Icon }) =>
              <div className="v2-high-risk-capability" key={title}>
                <Icon aria-hidden="true" size={25} />
                <div><strong>{title}</strong><span>{description}</span></div>
              </div>)}
          </div>
          <div className="v2-high-risk-scope" role="note">
            <strong><AlertTriangle aria-hidden="true" size={15} />影响范围</strong>
            <p id={boundaryID}>{copy.boundary}</p>
          </div>
          <div className="v2-high-risk-warning">
            <p>{copy.risk}</p>
            <details><summary>了解运行边界</summary>
              <p>{copy.details}</p>
            </details>
          </div>
          {error && <p className="v2-high-risk-error" role="alert">{error}</p>}
        </div>
        <footer className="v2-high-risk-actions">
          {busy && <span aria-live="polite" role="status">
            {phase === "restarting" ? "正在重启…" : phase === "applying"
              ? "正在应用…" : "等待系统确认…"}
          </span>}
          <div>
            <button disabled={busy} onClick={onCancel} ref={cancelRef} type="button">取消</button>
            <button className="danger" disabled={busy} type="submit">
              <AlertTriangle aria-hidden="true" size={15} />
              {phase === "restarting" ? "正在重启…" : phase === "confirming"
                ? "等待确认…" : phase === "applying" ? "正在应用…" : copy.confirmLabel}
            </button>
          </div>
        </footer>
      </form>
    </section>
  </div>;

  return createPortal(dialog, document.body);
}
