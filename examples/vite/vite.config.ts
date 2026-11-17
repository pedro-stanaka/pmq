import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const target = loadEnv(mode, process.cwd(), 'VITE_').VITE_PMQ_TARGET ?? 'http://sdk-test.apps.localhost:8080';
  const appOrigin = new URL(target).origin;
  return {
    server: {
      allowedHosts: ['.apps.localhost'],
      proxy: {
        '/_pmq': {
          target,
          changeOrigin: true,
          configure(proxy) {
            proxy.on('proxyReq', (proxyRequest, request) => {
              if (request.headers.origin) proxyRequest.setHeader('Origin', appOrigin);
            });
          },
        },
      },
    },
  };
});
