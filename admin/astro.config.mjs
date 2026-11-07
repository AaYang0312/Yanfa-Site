import http from 'node:http';
import { defineConfig } from 'astro/config';

const target = process.env.ADMIN_API_TARGET;
if (target) {
  const url = new URL(target);
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || url.pathname !== '/' || url.search || url.hash || url.username || url.password) {
    throw new Error('ADMIN_API_TARGET must be a loopback HTTP origin');
  }
}

// Astro prepends base/trailing-slash handlers ahead of Vite's ordinary proxy.
// Put the API stream first so /api keeps its exact paths, Origin and cookies.
const apiProxy = {
  name: 'admin-api-proxy',
  enforce: 'post',
  configureServer(server) {
    if (!target) return;
    return () => server.middlewares.stack.unshift({
      route: '',
      handle(request, response, next) {
        if (!/^\/api(?:\/|$)/.test(request.url ?? '')) return next();
        const upstream = http.request(new URL(request.url, target), {
          method: request.method,
          headers: request.headers,
        }, incoming => {
          response.writeHead(incoming.statusCode ?? 502, incoming.headers);
          incoming.pipe(response);
        });
        upstream.on('error', () => {
          if (response.headersSent) return response.destroy();
          response.writeHead(502, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
          response.end(JSON.stringify({ error: { code: 'API_UNAVAILABLE', message: '管理接口暂不可用。', fields: [] } }));
        });
        request.on('aborted', () => upstream.destroy());
        response.on('close', () => upstream.destroy());
        request.pipe(upstream);
      },
    });
  },
};
export default defineConfig({
  base: '/admin',
  outDir: new URL('./dist/', import.meta.url).pathname,
  cacheDir: new URL('../.work/admin-cache/', import.meta.url).pathname,
  output: 'static',
  trailingSlash: 'always',
  vite: {
    plugins: [apiProxy],
    server: {
      fs: { allow: [new URL('../', import.meta.url).pathname] },
    },
  },
});
