# Azure

Read secrets from **Azure Key Vault**, authenticated via an AAD client-credentials flow.

## Configuration

Under **Settings → External Resources → Add Resource → Azure**:

```text
Type        : Azure
VaultURL    : https://my-vault.vault.azure.net/
TenantID    : ...
ClientID    : ...
ClientSecret: ...
```

- **VaultURL** — the Key Vault URL, including the trailing slash.
- **TenantID** / **ClientID** / **ClientSecret** — credentials of an AAD application that has been granted `get` / `list` permissions on the vault's secrets (via Access Policies or RBAC).

::: warning
The client secret is stored in the database. Set the [encryption key](../encryption) so it's encrypted at rest.
:::

## Inheritance entry

`path` is the secret name.

```json
{
  "resource": "azure",
  "path": "myapp-db-password",
  "inject": "database.password"
}
```

See [Inheritance](./) for the full meaning of `paths` / `inject`.
