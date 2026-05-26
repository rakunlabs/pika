// Static form schemas for every proxy middleware and handler
// subtype. The backend ships a config_schema field on each catalog
// entry but those are intentionally permissive — this file is the
// canonical UI contract that drives the per-node form: we know
// every input type, every default value and every helper text,
// so the user never has to type raw JSON.
//
// Keep these in sync with the Go-side config structs (search for
// "type XxxCfg struct" in internal/server/proxy/middlewares.go and
// handlers.go). The runtime parser already rejects unknown fields
// politely, but mis-typed defaults here would silently break a
// node when the operator hits Save.

export type FieldKind =
  | 'string'
  | 'text'        // multi-line string
  | 'number'
  | 'boolean'
  | 'string-list' // comma-separated
  | 'select'
  | 'kv-map';     // simple key=value pairs

export interface FieldDef {
  key: string;
  label: string;
  kind: FieldKind;
  placeholder?: string;
  help?: string;
  default?: unknown;
  required?: boolean;
  options?: { value: string; label: string }[];
}

export interface FormDef {
  // Optional friendly intro shown above the form.
  intro?: string;
  fields: FieldDef[];
}

// PATH_FIELD used to live here back when each handler owned its
// own mount path. Path matching moved to the switch node — handlers
// now see r.URL.Path exactly as the listener received it (minus any
// explicit strip_prefix below). The shared field is gone; if a
// future handler needs its own path-ish field it should declare
// that field locally and explicitly.

const STRIP_PREFIX_FIELD: FieldDef = {
  key: 'strip_prefix',
  label: 'Strip prefix',
  kind: 'string',
  placeholder: '/api',
  help: 'Removed from r.URL.Path before the handler runs. Empty = handler sees the full path. Useful when a switch routes /api/* to this handler but the handler expects URLs starting at /.',
};

const REGISTRY_STRIP_PREFIX_FIELD: FieldDef = {
  ...STRIP_PREFIX_FIELD,
  placeholder: '/npm',
  help: 'Removed before the registry handles the request, and also used as the public mount point when registry responses emit absolute URLs. Empty = dedicated root-port registry.',
};

// ── Middlewares ─────────────────────────────────────────────────

export const middlewareForms: Record<string, FormDef> = {
  logger: {
    intro: 'Emits a slog Debug line per request — same format as the main pika listener uses.',
    fields: [],
  },

  requestid: {
    intro: 'Generates or passes through X-Request-Id on every request.',
    fields: [],
  },

  cors: {
    fields: [
      { key: 'allow_origins', label: 'Allow origins', kind: 'string-list',
        placeholder: '* or https://example.com,https://other.example.com',
        help: 'Empty = allow all.' },
      { key: 'allow_methods', label: 'Allow methods', kind: 'string-list',
        placeholder: 'GET,POST,PUT,DELETE,OPTIONS' },
      { key: 'allow_headers', label: 'Allow headers', kind: 'string-list',
        placeholder: 'Content-Type,Authorization' },
      { key: 'expose_headers', label: 'Expose headers', kind: 'string-list' },
      { key: 'allow_credentials', label: 'Allow credentials', kind: 'boolean', default: false },
      { key: 'max_age', label: 'Max age (seconds)', kind: 'number', placeholder: '600' },
    ],
  },

  'auth-bearer': {
    intro: 'Requires a pika API token in the Authorization header. The token is validated by the same engine that protects /data and /raw.',
    fields: [
      { key: 'scope', label: 'Token scope override', kind: 'string',
        placeholder: '(default: request URL path)',
        help: 'When empty the scope defaults to the incoming request path so tokens with path-prefix scopes work transparently.' },
      { key: 'operation', label: 'Operation override', kind: 'select',
        help: 'When empty the operation is derived from the HTTP method.',
        options: [
          { value: '', label: '(derive from HTTP method)' },
          { value: 'read', label: 'read' },
          { value: 'write', label: 'write' },
          { value: 'delete', label: 'delete' },
        ] },
    ],
  },

  'basic-auth': {
    intro: 'Static user list checked against HTTP Basic credentials. Passwords are stored as htpasswd-style hashes (bcrypt, apr1, SHA, crypt) — never plaintext.',
    fields: [
      { key: 'realm', label: 'Realm', kind: 'string', placeholder: 'pika-proxy', default: 'pika-proxy' },
      { key: 'users', label: 'Users (one per line as "username:hash")', kind: 'text',
        placeholder: 'alice:$2y$12$...\nbob:$apr1$...',
        help: 'Generate with htpasswd: `htpasswd -nB <user>` for bcrypt or `htpasswd -n <user>` for apr1. At least one entry required.' },
      { key: 'header_field', label: 'Forward username header', kind: 'string', placeholder: 'X-User',
        help: 'When set, the authenticated username is added to this request header before forwarding upstream.' },
      { key: 'remove_header', label: 'Strip Authorization header', kind: 'boolean', default: false,
        help: 'Removes the Authorization header before the request reaches the upstream so credentials never leak past the proxy.' },
    ],
  },

  'ip-allowlist': {
    fields: [
      { key: 'cidrs', label: 'Allowed CIDRs (comma-separated)', kind: 'string-list',
        placeholder: '10.0.0.0/8, 192.168.1.5',
        help: 'A bare IP is accepted (treated as /32 / /128). Empty list rejects everything.' },
      { key: 'trust_forwarded_for', label: 'Trust X-Forwarded-For', kind: 'boolean', default: false,
        help: 'Off by default because trusting this header on a public listener lets any client spoof their address.' },
    ],
  },

  'ip-denylist': {
    fields: [
      { key: 'cidrs', label: 'Blocked CIDRs (comma-separated)', kind: 'string-list',
        placeholder: '203.0.113.0/24' },
      { key: 'trust_forwarded_for', label: 'Trust X-Forwarded-For', kind: 'boolean', default: false },
    ],
  },

  'header-inject': {
    fields: [
      { key: 'request', label: 'Request headers to add', kind: 'kv-map',
        help: 'Added to every incoming request before the next middleware.' },
      { key: 'response', label: 'Response headers to add', kind: 'kv-map',
        help: 'Added just before WriteHeader on the way out.' },
      { key: 'overwrite', label: 'Overwrite existing values', kind: 'boolean', default: false,
        help: 'Off = append (multi-value semantics). On = Set (replace).' },
    ],
  },

  'header-remove': {
    fields: [
      { key: 'request', label: 'Request headers to strip', kind: 'string-list', placeholder: 'X-Internal-Token' },
      { key: 'response', label: 'Response headers to strip', kind: 'string-list', placeholder: 'Server, X-Powered-By' },
    ],
  },

  ratelimit: {
    fields: [
      { key: 'window', label: 'Window (Go duration)', kind: 'string', placeholder: '1m', default: '1m', required: true },
      { key: 'soft_threshold', label: 'Soft threshold', kind: 'number',
        help: 'Number of requests inside the window after which backoff kicks in.' },
      { key: 'hard_threshold', label: 'Hard threshold', kind: 'number', required: true,
        help: 'Requests at or above this in one window are rejected with 429.' },
      { key: 'backoff_base', label: 'Backoff base (Go duration)', kind: 'string', placeholder: '100ms' },
      { key: 'backoff_max', label: 'Backoff max (Go duration)', kind: 'string', placeholder: '5s' },
      { key: 'key_by', label: 'Bucket by', kind: 'select', default: 'ip',
        options: [
          { value: 'ip', label: 'Client IP' },
          { value: 'ip+path', label: 'Client IP + URL path' },
          { value: 'header', label: 'Header value' },
        ] },
      { key: 'key_header', label: 'Header name (when bucket=header)', kind: 'string', placeholder: 'X-API-Key' },
      { key: 'trust_forwarded_for', label: 'Trust X-Forwarded-For', kind: 'boolean', default: false },
      { key: 'store_capacity', label: 'In-memory bucket capacity', kind: 'number', placeholder: '10000' },
    ],
  },

  compress: {
    fields: [
      { key: 'min_length', label: 'Min body length (bytes)', kind: 'number', default: 1024,
        help: 'Bodies smaller than this skip compression to avoid gzip overhead on tiny responses.' },
      { key: 'level', label: 'Gzip level (1–9)', kind: 'number', placeholder: '6',
        help: 'Default 6 (gzip.DefaultCompression).' },
    ],
  },

  timeout: {
    fields: [
      { key: 'duration', label: 'Per-request deadline (Go duration)', kind: 'string',
        default: '30s', required: true, placeholder: '30s' },
    ],
  },

  'request-size-limit': {
    fields: [
      { key: 'max_bytes', label: 'Max body bytes', kind: 'number', required: true,
        placeholder: '1048576',
        help: 'Requests with a body larger than this get http.MaxBytesReader semantics.' },
    ],
  },

  'strip-prefix': {
    intro: 'Remove a leading path segment from r.URL.Path before the next node sees it. Mirrors turna stripprefix.',
    fields: [
      { key: 'prefix', label: 'Prefix', kind: 'string', placeholder: '/api',
        help: 'Single prefix. Leave empty and use "Prefixes" below to try several in order.' },
      { key: 'prefixes', label: 'Prefixes (try in order)', kind: 'string-list',
        placeholder: '/api,/v1',
        help: 'First match wins. Used when one node should peel different mount points.' },
      { key: 'force_slash', label: 'Re-add leading slash if stripped', kind: 'boolean', default: true,
        help: 'On (default): result always starts with "/". Off: literal trim, occasionally needed before proxy-pass.' },
    ],
  },

  'add-prefix': {
    intro: 'Prepend a fixed prefix to r.URL.Path. Mirrors turna addprefix — handy when a switch routes /foo into a node whose downstream expects /api/foo.',
    fields: [
      { key: 'prefix', label: 'Prefix', kind: 'string', placeholder: '/api',
        help: 'Prepended via url.JoinPath, so duplicate slashes collapse correctly. Empty = no-op.' },
    ],
  },

  'regex-path': {
    intro: 'Rewrite r.URL.Path with a Go RE2 regex. Uses $1 / $2 / ${name} expansion (NOT Perl-style \\1). Compile errors surface on Save.',
    fields: [
      { key: 'regex', label: 'Regex', kind: 'string', placeholder: '^/old/(.*)$',
        help: 'Go RE2 syntax. An always-empty match would loop; pika rejects cycles at compile time but mind the replacement chain.' },
      { key: 'replacement', label: 'Replacement', kind: 'string', placeholder: '/new/$1',
        help: 'Use $1, $2, ${name} to reference capture groups.' },
    ],
  },

  'header-compare': {
    intro: 'Allow or block requests based on header values. Use kv-map shorthand "X-Tenant: acme" for exact match; switch to Advanced JSON for regex matchers.',
    fields: [
      { key: 'mode', label: 'Mode', kind: 'select', default: 'allow',
        options: [
          { value: 'allow', label: 'allow — every header must match' },
          { value: 'block', label: 'block — reject when every header matches' },
        ],
      },
      { key: 'status', label: 'Failure status', kind: 'number', default: 403, placeholder: '403',
        help: 'HTTP code returned on the non-passing branch.' },
      { key: 'headers', label: 'Headers (name → expected value)', kind: 'kv-map',
        help: 'AND-ed. Empty value means "header just has to be present". For regex matchers use the Advanced JSON editor.' },
    ],
  },

  'response-rewrite': {
    intro: 'Buffer the downstream response and tweak status, headers or body before it leaves the listener. Body buffering is capped at 1 MiB by default.',
    fields: [
      { key: 'set_status', label: 'Force status code', kind: 'number', placeholder: '200',
        help: 'When set, the upstream status is replaced wholesale.' },
      { key: 'status_map', label: 'Status remap (upstream → outgoing)', kind: 'kv-map',
        help: 'Cheaper than full set_status when only a few upstream codes need translating (e.g. 502 → 503).' },
      { key: 'set_headers', label: 'Set response headers', kind: 'kv-map',
        help: 'Overwrites whatever the upstream sent for that header.' },
      { key: 'delete_headers', label: 'Strip response headers', kind: 'string-list',
        placeholder: 'Set-Cookie, X-Powered-By' },
      { key: 'body_override', label: 'Replace body wholesale', kind: 'text',
        placeholder: '{"error":"masked"}',
        help: 'When set, the upstream body is discarded. Pair with content_type via set_headers.' },
      { key: 'max_body_bytes', label: 'Max buffered body bytes', kind: 'number',
        placeholder: '1048576',
        help: 'Responses larger than this stream through unchanged. Body knobs no-op for oversize responses.' },
    ],
  },

  'tcp-ip-allowlist': {
    intro: 'TCP source filter. The connection is closed before reaching the terminal TCP handler unless the client IP matches one of these networks.',
    fields: [
      { key: 'cidrs', label: 'Allowed CIDRs', kind: 'string-list',
        placeholder: '10.0.0.0/8, 192.168.1.5',
        help: 'A bare IP is accepted and treated as /32 or /128. Empty list rejects everything.' },
    ],
  },

  'tcp-ip-denylist': {
    intro: 'TCP source filter. Connections from these networks are closed before reaching the terminal TCP handler.',
    fields: [
      { key: 'cidrs', label: 'Blocked CIDRs', kind: 'string-list',
        placeholder: '203.0.113.0/24' },
    ],
  },
};

// ── Handlers ────────────────────────────────────────────────────

export const handlerForms: Record<string, FormDef> = {
  data: {
    intro: 'Serves resolved configuration files through the same engine as the main /data endpoint.',
    fields: [
      STRIP_PREFIX_FIELD,
      { key: 'default_format', label: 'Default format', kind: 'select', default: '',
        help: 'Used when the client does not pass ?format=.',
        options: [
          { value: '', label: '(use stored format)' },
          { value: 'json', label: 'json' },
          { value: 'yaml', label: 'yaml' },
          { value: 'toml', label: 'toml' },
        ] },
    ],
  },

  raw: {
    intro: 'Serves one configured Raw mount through this proxy listener. Add auth middleware in front if the listener is public.',
    fields: [
      { key: 'mount', label: 'Raw mount', kind: 'string', required: true,
        placeholder: 'assets',
        help: 'Must match a prefix in Settings → Raw mounts.' },
      STRIP_PREFIX_FIELD,
      { key: 'directory_listing', label: 'Enable directory listing', kind: 'boolean', default: false,
        help: 'When on, directory paths return a JSON list. When off, directories return 403.' },
      { key: 'allow_write', label: 'Allow PUT / DELETE / mkdir', kind: 'boolean', default: false,
        help: 'Off by default. When on, PUT writes files, DELETE removes files, and POST creates a directory on writable mounts.' },
    ],
  },

  registry: {
    intro: 'Publishes one existing artifact registry repository on this proxy listener. The request path after Strip prefix is passed to the registry implementation.',
    fields: [
      { key: 'namespace', label: 'Namespace', kind: 'string', required: true,
        placeholder: 'default' },
      { key: 'repository', label: 'Repository', kind: 'string', required: true,
        placeholder: 'npm-local' },
      REGISTRY_STRIP_PREFIX_FIELD,
      { key: 'require_token', label: 'Require pika token', kind: 'boolean', default: true,
        help: 'On by default to preserve the normal /registries token model. Turn off only if another middleware enforces access.' },
    ],
  },

  cdn: {
    intro: 'Publishes jsDelivr-style package file URLs from one existing NPM registry repository through this proxy listener.',
    fields: [
      { key: 'namespace', label: 'Namespace', kind: 'string', required: true,
        placeholder: 'default' },
      { key: 'repository', label: 'NPM repository', kind: 'string', required: true,
        placeholder: 'npm' },
      { key: 'strip_prefix', label: 'Strip prefix', kind: 'string',
        placeholder: '/npm',
        help: 'Removed before parsing the jsDelivr-style package path. Example: /npm/lodash@4.17.21/lodash.js becomes lodash@4.17.21/lodash.js.' },
      { key: 'require_token', label: 'Require pika token', kind: 'boolean', default: false,
        help: 'Off by default for browser/runtime asset fetches. Turn on, or add auth middleware, for private CDN paths.' },
    ],
  },

  external: {
    intro: 'Reads (and optionally writes) entries from a configured external resource — Vault, Consul, etcd, AWS, GCP, Azure, K8s, HTTP.',
    fields: [
      { key: 'resource', label: 'Resource name', kind: 'string', required: true,
        placeholder: 'vault-prod',
        help: 'Must match a key in Settings → External Resources.' },
      STRIP_PREFIX_FIELD,
      { key: 'allow_write', label: 'Allow PUT / DELETE', kind: 'boolean', default: false,
        help: 'Off by default. When on, PUT writes back through WriteExternal and DELETE calls DeleteExternal.' },
    ],
  },

  'consul-kv': {
    intro: 'Exposes pika configurations under the Consul /v1/kv API shape so existing consul-template tooling can read them unchanged. The handler strips a "/v1/kv" prefix by default; override below if you mounted it under a different path.',
    fields: [
      { key: 'strip_prefix', label: 'Strip prefix', kind: 'string',
        placeholder: '/v1/kv',
        help: 'Default is "/v1/kv". Override when the switch in front routes a longer prefix into this handler.' },
    ],
  },

  healthz: {
    fields: [
      { key: 'body', label: 'Response body', kind: 'string', default: 'OK', placeholder: 'OK' },
    ],
  },

  'static-response': {
    intro: 'Returns a fixed status + headers + body. Useful for mocks and stubs while you build something behind it.',
    fields: [
      { key: 'status', label: 'Status code', kind: 'number', default: 200, placeholder: '200' },
      { key: 'content_type', label: 'Content-Type', kind: 'string', placeholder: 'application/json' },
      { key: 'body', label: 'Body', kind: 'text', placeholder: '{ "ok": true }' },
      { key: 'headers', label: 'Additional headers', kind: 'kv-map' },
    ],
  },

  redirect: {
    fields: [
      { key: 'target', label: 'Target URL', kind: 'string', required: true,
        placeholder: 'https://example.com' },
      { key: 'status', label: 'Status code', kind: 'select', default: 302,
        options: [
          { value: '301', label: '301 Moved Permanently' },
          { value: '302', label: '302 Found' },
          { value: '307', label: '307 Temporary Redirect (preserves method)' },
          { value: '308', label: '308 Permanent Redirect (preserves method)' },
        ] },
      { key: 'preserve_path', label: 'Preserve path & query', kind: 'boolean', default: false,
        help: 'On → /old/foo?x=1 → <target>/foo?x=1.' },
      STRIP_PREFIX_FIELD,
    ],
  },

  'proxy-pass': {
    intro: 'Forwards every request to an upstream URL (httputil.ReverseProxy). WebSocket upgrades pass through transparently.',
    fields: [
      { key: 'target', label: 'Upstream URL', kind: 'string', required: true,
        placeholder: 'http://upstream:8080' },
      STRIP_PREFIX_FIELD,
      { key: 'preserve_host', label: 'Forward original Host header', kind: 'boolean', default: false,
        help: 'Off = rewrite Host to the upstream. On = preserve the incoming client Host.' },
      { key: 'set_request_headers', label: 'Set request headers', kind: 'kv-map',
        help: 'Applied to the forwarded request just before Send.' },
      { key: 'insecure_skip_tls', label: 'Insecure: skip TLS verification', kind: 'boolean', default: false,
        help: 'Only safe in dev. Production should fix the upstream certificate.' },
    ],
  },

  'custom-response': {
    intro:
      'Templated response. The body is a Go text/template with access to .Method, .Path, .RawQuery, .Host, .RemoteAddr, .Query.<key>, .Headers.<X_Forwarded_For> (hyphens → underscores), and .Now.',
    fields: [
      { key: 'status', label: 'Status code', kind: 'number', default: 200, placeholder: '200' },
      { key: 'content_type', label: 'Content-Type', kind: 'string', placeholder: 'application/json' },
      { key: 'body', label: 'Body template', kind: 'text',
        placeholder: '{ "path": "{{.Path}}", "tenant": "{{.Headers.X_Tenant}}" }',
        help: 'Empty body is allowed. Template parse errors surface as compile errors when you Save.' },
      { key: 'headers', label: 'Additional response headers', kind: 'kv-map' },
    ],
  },

  'tcp-forward': {
    intro: 'Forwards one raw TCP connection to an upstream TCP, Unix, or UDP address. This is the TCP terminal node, equivalent to turna redirect.',
    fields: [
      { key: 'network', label: 'Upstream network', kind: 'select', default: 'tcp',
        options: [
          { value: 'tcp', label: 'tcp' },
          { value: 'tcp4', label: 'tcp4' },
          { value: 'tcp6', label: 'tcp6' },
          { value: 'unix', label: 'unix' },
          { value: 'unixpacket', label: 'unixpacket' },
          { value: 'udp', label: 'udp' },
          { value: 'udp4', label: 'udp4' },
          { value: 'udp6', label: 'udp6' },
        ] },
      { key: 'address', label: 'Upstream address', kind: 'string', required: true,
        placeholder: '127.0.0.1:5432 or /var/run/docker.sock' },
      { key: 'dial_timeout', label: 'Dial timeout', kind: 'string', placeholder: '5s',
        help: 'Go duration string. Empty means no explicit dial timeout.' },
      { key: 'buffer', label: 'Copy buffer bytes', kind: 'number', placeholder: '65535' },
      { key: 'disable_nagle', label: 'Disable Nagle (TCP_NODELAY)', kind: 'boolean', default: false },
      { key: 'proxy_protocol', label: 'Send PROXY protocol v1 header', kind: 'boolean', default: false,
        help: 'Only applies to TCP upstreams.' },
    ],
  },
};

// formFor resolves the right schema for a node by (type, subtype),
// returning null when the node has no editable config (listener,
// router) or when we shipped a new backend kind without registering
// a form here — in that case the page falls back to the raw JSON
// editor so the user is never blocked.
export function formFor(nodeType: string, subtype: string | undefined): FormDef | null {
  if (!subtype) return null;
  if (nodeType === 'middleware') return middlewareForms[subtype] ?? null;
  if (nodeType === 'handler') return handlerForms[subtype] ?? null;
  return null;
}
