"use strict";

const fs = require("fs");
for (const name of ["HOME", "SSH_AUTH_SOCK", "DOCKER_HOST", "GIT_ASKPASS", "AWS_ACCESS_KEY_ID"]) {
  if (process.env[name]) throw new Error(`host credential environment leaked through ${name}`);
}
for (const name of ["/var/run/docker.sock", "/root/.ssh", "/host"]) {
  if (fs.existsSync(name)) throw new Error(`undeclared host path is visible: ${name}`);
}
if (fs.readFileSync("../.git", "utf8") !== "gitdir: .traverse-board-git-disabled\n") {
  throw new Error("linked-worktree host metadata was not masked");
}
fs.writeFileSync("node-output.txt", "node offline fixture\n", { mode: 0o600 });
