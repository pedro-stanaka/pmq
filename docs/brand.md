# PMQ visual language

“Quiet infrastructure” makes the work feel clear and dependable. Use the sign-off “Build it. Drop it. Run it.” and reserve the mint accent for actions, links and meaningful states. Avoid decorative gradients and invented operational metrics.

The docs theme (`docs/site/.vitepress/theme/style.css`) and embedded console (`internal/pmq/console.html`) use the same core palette: dark background `#0c171b`, panel `#132328`, text `#eef5ef`, muted text `#a9bdb9`, border `#2b4142`, accent `#94d9bc`; light background `#f6f8f5`, panel `#ffffff`, text `#14252a`, muted text `#4f6265`, border `#d4dfda`, accent `#176a59`. Keep both copies in sync: the console must work offline.

Use the system sans-serif stack for interface and reading text, and the system monospace stack for code and release IDs. Prefer restrained weights, readable line lengths, an 8px spacing rhythm, 8px controls and 12px panels. Use borders for structure, visible focus rings and reduced-motion support. The three-bar mark works on either theme in a dark square.

## Exercising the console locally

From the repository root, run `make build`, set `PMQ_DEPLOY_TOKEN` to a new random value, and start `./bin/pmq serve --dev --listen 127.0.0.1:8080 --domain apps.localhost --data-dir ./data`. Open `http://apps.localhost:8080`, connect with the token, and upload a zip containing `index.html` at its root. Deploy a second version under the same name, expand Releases, and activate the earlier release to exercise rollback. Stop the server and remove the disposable data directory when finished. See the first-app guide for the complete walkthrough.
