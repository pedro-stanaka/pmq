# PMQ app validation checklist

Use this before declaring a generated app ready.

## Build and deploy

- [ ] Build succeeds from a clean install with the chosen stack's normal production command.
- [ ] Deploy the actual output directory (`dist/` for Vite), not source files or the repository root.
- [ ] Confirm `index.html` and referenced assets load from the deployed app hostname.
- [ ] If using client-side routes, open and refresh a nested route on PMQ.

## Runtime and APIs

- [ ] Exercise the main create/read/update/delete flow against `/_pmq/db/*`; verify data survives a page reload and a frontend redeploy.
- [ ] If the app uses objects, upload, retrieve, list and delete an object; test empty and binary files where relevant.
- [ ] If the app uses AI, test with a configured server-side provider and alias, plus an unavailable-provider response. Never require a provider key in the browser.
- [ ] Check loading, empty, success, invalid-input, unauthorized, not-found, oversized and provider-error states as applicable.
- [ ] Keep the form input or offer a retry after a failed request. Do not report success before the response succeeds.

## Browser and security

- [ ] Use only same-origin relative API URLs.
- [ ] Verify ordinary browser writes work through Origin protection. Do not add identity headers or bypass CSRF.
- [ ] Confirm no deployment token, proxy secret, API key, private config or internal PMQ URL appears in source maps or built assets.
- [ ] Test at the target viewport and with keyboard navigation; give controls labels, errors an announced region, and state changes visible feedback.
- [ ] Verify the app makes no request to an unsupported endpoint or unconfigured third-party service.
