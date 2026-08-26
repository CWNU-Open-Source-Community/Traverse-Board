## Summary

- Describe the user-visible or architectural change.
- Explain why this slice is needed now.

## Validation

- [ ] `go test -count=1 ./...`
- [ ] `go vet ./...`
- [ ] Relevant CLI smoke tests

## Surface governance

- [ ] No Surface is added, promoted, downgraded, deprecated, or removed.
- [ ] If the checkbox above is not applicable, every declaration below is complete
  and `docs/convergence/surface-registry.json` plus its generated inventory are
  included in this PR.

<!-- Use "N/A — no Surface change" only when the first checkbox is checked. -->

- **Registry item(s):**
- **Target tier / transition:**
- **Entry criteria / decision:**
- **Owner:**
- **Shared Go Application contract:**
- **Authority impact:**
- **Supported platforms:**
- **Release / test evidence:**
- **Compatibility strategy:**
- **Deprecation window:**
- **Removal / rollback plan:**

Maintenance-only entries accept only security, compatibility, data-loss, and severe
defect fixes; a new workflow requires promotion first. Extension-only entries must
remain optional, non-release-blocking, and behind Go-owned policy, approval, and
Scope.

## Audit

- [ ] No credentials or local runtime data are included.
- [ ] Policy, workspace, sandbox, and persistence boundaries were reviewed.
- [ ] `README.md` or project memory was updated when behavior or progress changed.
