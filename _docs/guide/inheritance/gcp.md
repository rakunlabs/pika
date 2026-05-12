# GCP

Read secrets from **Google Cloud Secret Manager**.

## Configuration

Under **Settings → External Resources → Add Resource → GCP**:

```text
Type               : GCP
ServiceAccountJSON : { "type": "service_account", ... }   (full JSON key, pasted)
```

- **ServiceAccountJSON** — the full JSON key of a service account that has the `roles/secretmanager.secretAccessor` role on the secrets pika should read. Pika derives the GCP project ID from this JSON.

::: warning
The service-account JSON is stored in the database. Set the [encryption key](../encryption) so it's encrypted at rest.
:::

## Inheritance entry

`path` is the secret name. Pika resolves the latest version automatically.

```json
{
  "resource": "gcp",
  "path": "myapp-db-password",
  "inject": "database.password"
}
```

See [Inheritance](./) for the full meaning of `paths` / `inject`.
