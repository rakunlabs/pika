# GCP Parameter Manager

Read parameters from **Google Cloud Parameter Manager**.

Unlike Secret Manager, Parameter Manager is **location-scoped**: every parameter lives under `projects/{project}/locations/{location}/parameters/{name}`. Pika defaults to the `global` location and lets you override it per resource.

## Configuration

Under **Settings → External Resources → Add Resource → GCP Parameter**:

```text
Type               : GCP Parameter
ServiceAccountJSON : { "type": "service_account", ... }   (full JSON key, pasted)
Location           : global                                (or e.g. us-central1)
```

- **ServiceAccountJSON** — the full JSON key of a service account with the `roles/parametermanager.parameterAccessor` role on the parameters Pika should read. Pika derives the GCP project ID from this JSON.
- **Location** — defaults to `global` when empty. Use a regional location only if your parameters are regional. Pika automatically targets the matching regional endpoint (`parametermanager.<region>.rep.googleapis.com`) so cross-region requests don't fall back to the global endpoint, which rejects them with a misleading 403.

::: warning
The service-account JSON is stored in the database. Set the [encryption key](../encryption) so it's encrypted at rest.
:::

## Inheritance entry

`path` is the parameter name. Pika hits the `:render` endpoint, so JSON/YAML parameters that reference Secret Manager versions are resolved server-side in a single round trip.

```json
{
  "resource": "myapp-params",
  "path": "myapp-config",
  "inject": "database"
}
```

By default Pika reads the `latest` version. To pin a specific version, use either form:

```json
{ "path": "myapp-config/versions/v3" }
{ "path": "myapp-config/v3" }
```

### Payload formats

Parameter Manager parameters have a format (UNFORMATTED, JSON, YAML):

- **JSON / YAML** — Pika parses the rendered payload into a map and merges its fields the same way Secret Manager JSON secrets do.
- **UNFORMATTED** — Pika returns `{"value": "<string>"}` so callers can still `inject` it into a single config field.

See [Inheritance](./) for the full meaning of `paths` / `inject`.

## Differences from GCP Secret Manager

| | Secret Manager | Parameter Manager |
|---|---|---|
| Resource shape | `projects/p/secrets/X` | `projects/p/locations/L/parameters/X` |
| Location | n/a (global) | required, default `global` |
| IAM role | `roles/secretmanager.secretAccessor` | `roles/parametermanager.parameterAccessor` |
| Secret references | resolved by caller | resolved by `:render` server-side |
| Version history in browser | No | Yes — newest-first strip, disabled versions painted amber |
| Writable from Pika | No | No |

Both backends share the same JWT/OAuth2 token exchange (scope `cloud-platform`), so the same service account can drive both — just grant the matching IAM roles.
