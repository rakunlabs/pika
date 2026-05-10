# Raw file serving

Beyond the config store, pika can mount external storage backends and serve them as plain files over HTTP, FTP, SFTP, TFTP, or WebDAV. This is useful for static assets, certificates, firmware blobs, or anything that doesn't need versioning.

## Backends

Configure mounts under **Settings → Raw Mounts**. Each mount has a **prefix** (used in URLs) and a backend type:

| Backend         | Description                                                                                       |
| --------------- | ------------------------------------------------------------------------------------------------- |
| **Local**       | A directory on disk. Also works on top of FUSE-mounted remote filesystems (s3fs, rclone, gcsfuse). |
| **S3**          | AWS S3, MinIO, Cloudflare R2, DigitalOcean Spaces, or any S3-compatible store. Read + write.       |
| **FTP / FTPS**  | Connect to a remote (S)FTP server.                                                                 |
| **SFTP**        | Connect to a remote SSH / SFTP server.                                                             |
| **WebDAV**      | Connect to a WebDAV server.                                                                        |
| **Vercel Blob** | Read / write Vercel Blob storage.                                                                  |

The mount form has conditional fields per backend (host, credentials, region, bucket, etc.). Saved mounts are usable immediately — no restart.

## HTTP API

Files are served at `/raw/{prefix}/{path}`.

```sh
# Read (main server — Bearer token required)
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/raw/configs/app.json

# Read (public server — no auth)
curl http://localhost:9090/raw/configs/app.json

# Directory listing
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/raw/configs/

# Upload (S3 / WebDAV / SFTP / Vercel Blob — write scope required)
curl -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @app.json \
  http://localhost:8080/raw/assets/app.json

# Delete (delete scope required)
curl -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/raw/assets/app.json
```

Directory listings return a JSON array:

```json
[
  { "name": "app.json", "is_dir": false, "size": 1234 },
  { "name": "subdir",   "is_dir": true,  "size": 0 }
]
```

## Token scopes for raw mounts

Token scopes match against `raw/{prefix}/{path}`. Examples:

- `raw/**` — every file in every mount.
- `raw/configs/**` — everything under the `configs` mount.
- `raw/uploads/2025/**` — only the 2025 subtree of `uploads`.

Write operations need `write`, deletes need `delete`. See [Tokens & scopes](/reference/tokens-and-scopes).

## File browser UI

When at least one raw mount exists, a **Files** link appears in the navigation bar. The browser provides:

- **Tree navigation** — mounts as top-level nodes, with backend-type badges; directories expand on click.
- **Smart previews** based on extension:
  - **Text / code** — syntax-highlighted read-only editor for known formats.
  - **Images** — inline preview (PNG, JPG, SVG, WebP, …).
  - **Audio / video** — native browser player.
  - **PDF** — embedded viewer.
  - **Binary** — placeholder with an "Open Anyway" button that loads a hex dump.
- **Tabs** — multiple files open at once with right-click context menu.
- **File info panel** — name, mount, path, size, content type.
- **Download button** — always available for any file.
- **Large-file protection** — text preview truncated above 5 MB, hex dump capped at 10 MB.
- **Write operations** (where the backend supports it):
  - Upload, create folder, rename, copy, move, delete.

## Other protocols

The same raw mounts can also be served over FTP, SFTP, TFTP, and WebDAV. Each protocol has its own listener that you enable from **Settings**:

- **FTP / FTPS** — under `Settings → FTP Server`. Pick a port, optional TLS, anonymous mode, and which mounts to expose.
- **SFTP** — under `Settings → SFTP Server`. Generate or upload a host key. Authentication uses pika usernames + passwords.
- **TFTP** — under `Settings → TFTP Server`. UDP, no auth — meant for things like network-boot images on a trusted segment.
- **WebDAV** — under `Settings → WebDAV Server`. Uses HTTP basic auth backed by the same identity pool.

All four protocols read from and (where supported) write to the same set of raw mounts, with the same scope checks.

## Hooks on file changes

Every raw-file operation can fire an [event hook](./hooks). Useful for:

- Indexing uploads.
- Triggering a downstream pipeline when a new firmware blob lands.
- Audit logging.

The event payload includes the mount prefix, path, size, the protocol that triggered it (`http`, `ftp`, `sftp`, `webdav`, `tftp`), and the user.
