import { Container, Terminal, Box } from "lucide-react";
import type { SandboxBackend } from "../api/sandbox-environment";
import { useLocale } from "../lib/locale";

export const sandboxBackendLabels: Record<SandboxBackend, string> = {
  local: "Local", docker: "Docker Engine", sbx: "Docker Sandboxes (sbx)",
};

export function SandboxBackendSelector({ value, onChange, disabled = false, statuses }: {
  value: SandboxBackend;
  onChange: (backend: SandboxBackend) => void;
  disabled?: boolean;
  statuses?: Partial<Record<SandboxBackend, string>>;
}) {
  const { t } = useLocale();
  return <div aria-label={t("编码环境后端", "Coding backend")} className="permission-option-grid permission-option-grid-three sandbox-backend-selector" role="group">
    {(["local", "docker", "sbx"] as const).map((backend) => {
      const Icon = backend === "local" ? Terminal : backend === "docker" ? Container : Box;
      return <button aria-pressed={value === backend} disabled={disabled} key={backend}
        onClick={() => onChange(backend)} type="button">
        <Icon aria-hidden="true" size={17} /><span><strong>{sandboxBackendLabels[backend]}</strong>
          <small>{backend === "local" ? t("本机工作区", "Local workspace")
            : backend === "docker" ? t("离线 Linux 容器", "Offline Linux container") : t("官方 sbx 微虚拟机", "Official sbx microVM")}</small>
          {statuses?.[backend] && <small>{statuses[backend]}</small>}
        </span>
      </button>;
    })}
  </div>;
}
