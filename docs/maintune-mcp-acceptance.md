# Maintune web review acceptance fixture

This temporary document provides a small, complete diff for a real review
acceptance run. It does not change Traverse Board behavior or configuration.

The run checks that ChatGPT Chat can read a pending review through MCP,
prepare a review tied to the current commit, and submit it to Maintune.
Maintune publishes the result through the dedicated GitHub App.

The test pull request must remain unmerged. Acceptance evidence must identify
the reviewed commit and the resulting GitHub Review. A queued submission alone
does not establish that publication succeeded.

The PR is imported locally for this run. This does not verify public GitHub
Webhook delivery. No credentials or local runtime data belong in this fixture.
