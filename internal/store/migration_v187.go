package store

// Retain historical outcome values. A NULL rejection means that the old
// writer did not capture the decision; never infer it from a current manifest.
func pluginHookDiagnosticsStatements() []string {
	return []string{
		`ALTER TABLE plugin_hook_audits ADD COLUMN plugin_fingerprint TEXT NOT NULL DEFAULT '' CHECK(plugin_fingerprint = '' OR (length(plugin_fingerprint) = 64 AND plugin_fingerprint NOT GLOB '*[^0-9a-f]*'));`,
		`ALTER TABLE plugin_hook_audits ADD COLUMN declared_action TEXT NOT NULL DEFAULT '' CHECK(declared_action IN ('', 'deny', 'annotate', 'narrow', 'record'));`,
		`ALTER TABLE plugin_hook_audits ADD COLUMN rejected INTEGER CHECK(rejected IS NULL OR rejected IN (0, 1));`,
		`CREATE TRIGGER trg_plugin_hook_diagnostics_bound BEFORE INSERT ON plugin_hook_audits
		 WHEN (NEW.plugin_fingerprint != '' OR NEW.declared_action != '' OR NEW.rejected IS NOT NULL)
		 AND (NEW.plugin_fingerprint = '' OR NEW.declared_action = '' OR NEW.rejected IS NULL
		 OR NEW.rejected != CASE WHEN NEW.outcome = 'failed_closed' OR (NEW.declared_action = 'deny' AND NEW.outcome = 'completed') THEN 1 ELSE 0 END)
		 BEGIN SELECT RAISE(ABORT, 'Hook decision metadata must bind one observed outcome'); END;`,
		`CREATE INDEX idx_plugin_hook_audits_workspace_created ON plugin_hook_audits(workspace_id, created_at DESC, id DESC);`,
	}
}
