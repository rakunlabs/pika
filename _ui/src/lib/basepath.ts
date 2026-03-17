// Derive the base path from where the page is served.
// When base_path is configured (e.g., "/pika"), the index.html is served at
// "/pika/" and all API routes live under "/pika/api/v1/...".
// With Vite's `base: './'`, we can compute this from the page URL.
function getBasePath(): string {
  // pathname before the hash, e.g., "/pika/" -> "/pika"
  let path = window.location.pathname;
  // Remove trailing slash
  if (path.endsWith('/')) {
    path = path.slice(0, -1);
  }
  return path;
}

export const basePath = getBasePath();
