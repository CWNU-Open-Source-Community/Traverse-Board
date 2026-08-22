import os
from pathlib import Path

for name in ("HOME", "SSH_AUTH_SOCK", "DOCKER_HOST", "GIT_ASKPASS", "AWS_ACCESS_KEY_ID"):
    if os.environ.get(name):
        raise RuntimeError(f"host credential environment leaked through {name}")
for name in ("/var/run/docker.sock", "/root/.ssh", "/host"):
    try:
        Path(name).stat()
    except (FileNotFoundError, PermissionError):
        continue
    raise RuntimeError(f"undeclared host path is visible: {name}")
Path("python-output.txt").write_text("python offline fixture\n", encoding="utf-8")
