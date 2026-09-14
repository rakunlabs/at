import { readFile, readdir } from 'node:fs/promises';

const assetsRoot = new URL('../assets/', import.meta.url);
const offlineTemplate = new URL('./public/offline.html', import.meta.url);

/** Keep repository branding as the only source for both dev and embedded builds. */
export async function loadBrandAssets() {
  const files = new Map();
  for (const name of await readdir(assetsRoot)) {
    if (!/^favicon(?:-\d+x\d+)?\.(?:svg|png|ico)$/.test(name)) continue;
    files.set(`brand/${name}`, await readFile(new URL(name, assetsRoot)));
  }
  const logo = files.get('brand/favicon.svg');
  if (!logo || !files.has('brand/favicon.ico')) throw new Error('Missing branding in assets/');
  // Retain the conventional browser fallback URL, using the same source.
  files.set('favicon.ico', files.get('brand/favicon.ico'));
  // A data URL keeps the offline page self-contained; the worker caches only HTML.
  const offline = (await readFile(offlineTemplate, 'utf8'))
    .replace('__AT_BRAND_LOGO__', `data:image/svg+xml;base64,${logo.toString('base64')}`);
  files.set('offline.html', Buffer.from(offline));
  return files;
}

export function brandAssets() {
  return {
    name: 'at-brand-assets',
    async buildStart() {
      for (const name of await readdir(assetsRoot)) this.addWatchFile(new URL(name, assetsRoot).pathname);
      this.addWatchFile(offlineTemplate.pathname);
    },
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        const path = new URL(req.url || '/', 'http://localhost').pathname.slice(1);
        if (!(path.startsWith('brand/') || path === 'favicon.ico' || path === 'offline.html')) return next();
        if (req.method !== 'GET' && req.method !== 'HEAD') return next();
        try {
          const data = (await loadBrandAssets()).get(path);
          if (!data) { res.statusCode = 404; res.end(); return; }
          const ext = path.split('.').pop();
          res.setHeader('Content-Type', { svg: 'image/svg+xml', png: 'image/png', ico: 'image/x-icon', html: 'text/html; charset=utf-8' }[ext]);
          res.setHeader('Cache-Control', 'no-cache');
          res.end(req.method === 'HEAD' ? undefined : data);
        } catch (error) { next(error); }
      });
    },
    async generateBundle() {
      for (const [fileName, source] of await loadBrandAssets()) {
        this.emitFile({ type: 'asset', fileName, source });
      }
    },
  };
}
