# Instance dashboards on the existing webserver

User direction October 28, 2026: serve the generated HTML using the existing machine webserver and name each page for its instance.

1. Inspect existing service/root read-only. Current verified target is the running CondoCodex IIS CodexProjectDeck site, root C:/ProgramData/FounderOS/CodexProjectDeck/web, loopback HTTP port39119. Existing permissions allow the current user to write static files. No new service, binding, firewall rule, privilege elevation or site configuration.
2. Configure dashboard.file as {instance}.html, dashboard.instance_name empty by default (exact run-folder basename), and dashboard.publish_directory as the verified webroot. Preserve readable names and spaces; URL-encode the filename for links. Allow an explicit instance name for intentionally named instances. No hashes or naming ledger.
3. Render one operational snapshot and atomically write its identical bytes locally and to the configured existing publish directory on each normal refresh/final update. Publish only the generated HTML, never the runtime folder/config/database/logs. Page and tab display instance identity.
4. Focused tests prove independent names for different instances, exact published bytes, refreshed content, read-only operational state and valid browser script. Build once after those checks pass.
5. Use the program's dashboard command with -dir pointing at completed run h and -config pointing at the current source config; this updates only diagnostics and generated HTML, never invokes Meta or changes the saved request. Existing saved runtime config/executable need not be replaced.
6. Verify the deployed HTTP response matches the generated page, then inspect the authorized HTTP page and Protein tab in the browser. Record URLs, publication result and any remaining UI limitation. Future instances launched with this configuration publish through the same program-owned process.
