// Derive the base path from where the page is served.
// When base_path is configured (e.g., "/pika"), the index.html is served at
// "/pika/" and all API routes live under "/pika/api/v1/...".
// With Vite's `base: './'`, we can compute this from the page URL.
function getBasePath(): string {
  let path = window.location.pathname;

  if (path.length > 1 && path.endsWith('/')) {
    path = path.slice(0, -1);
  }

  // Auth redirects can land on the SPA shell at /{base}/login. That final
  // segment is an app route, not part of server.base_path.
  if (path === '/login') return '';
  if (path.endsWith('/login')) return path.slice(0, -'/login'.length);

  return path === '/' ? '' : path;
}

export const basePath = getBasePath();

export function withBasePath(path: string): string {
  if (!path.startsWith('/') || path.startsWith('//') || basePath === '') {
    return path;
  }
  if (path === basePath || path.startsWith(`${basePath}/`)) {
    return path;
  }
  return `${basePath}${path}`;
}

export function withoutBasePath(path: string): string {
  if (basePath === '' || !path.startsWith('/') || path.startsWith('//')) {
    return path;
  }
  if (path === basePath) {
    return '/';
  }
  if (path.startsWith(`${basePath}/`)) {
    return path.slice(basePath.length);
  }
  return path;
}
