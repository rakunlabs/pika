# HTTP

Generic HTTP fetcher. Useful when the source isn't one of the named backends — point pika at any HTTP endpoint that returns JSON / YAML / TOML and the response gets merged into the resolved config.

## Configuration

Under **Settings → External Resources → Add Resource → HTTP**:

```text
Type: HTTP
URL : https://example.com/configs/{path}
Auth: bearer / basic / oauth2 / none
```

Supports:

- **Basic auth** — username + password.
- **Bearer token** — static `Authorization: Bearer …` header.
- **OAuth2** — client credentials, password, and refresh flows.
- **Custom headers** — arbitrary header set sent with every request.
- **Retries** — configurable retry count / backoff for transient failures.

The `{path}` placeholder in the URL is replaced with the `path` from the inheritance entry, so a single resource can serve many configs.

## Inheritance entry

```json
{
  "resource": "my-http",
  "path": "myapp/production.json",
  "paths": ["features"],
  "inject": "features"
}
```

With the URL template `https://example.com/configs/{path}`, this fetches `https://example.com/configs/myapp/production.json`, picks out the `features` key, and injects it at `features` in the resolved config.

See [Inheritance](./) for the full meaning of `paths` / `inject`.
