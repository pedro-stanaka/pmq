# PMQ Vite example

This small app demonstrates the PMQ browser SDK with document persistence and AI. It expects a PMQ server listening on `127.0.0.1:8080` and an app deployed as `sdk-test` on the `apps.localhost` domain. Start the PMQ server with `--dev` and an AI model alias `fast` if you want to try the AI button.

From the repository root:

```sh
npm install
npm run dev --workspace @pmq-dev/example-vite
```

Open the Vite URL. Vite proxies `/_pmq/*` to PMQ using the application hostname and matching Origin. This is development-only proxying; production app requests should go directly to their PMQ application origin through the configured authenticated reverse proxy.

Use `http://sdk-test.apps.localhost:5173` in the browser (the `.localhost` name resolves to loopback). The proxy rewrites the request Host and Origin to the target PMQ app origin so PMQ's origin check passes. To use another local PMQ origin, set `VITE_PMQ_TARGET`, for example `VITE_PMQ_TARGET=http://my-app.apps.localhost:8080 npm run dev --workspace @pmq-dev/example-vite`. `--dev` provides the explicit loopback-only development identity; never use that mode on a network-accessible deployment.
