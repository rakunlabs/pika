# GitLab Group Variables

Browse and edit a GitLab group's CI/CD variables from **External**. Both GitLab.com and self-managed instances are supported.

## Configuration

Choose **GitLab Group Variables** when adding an external resource, either on the External page or under **Settings → External Resources**:

```text
Name             : gitlab-team
GitLab URL       : https://gitlab.com
Group ID or path : my-team/subgroup
Access token     : (personal or group access token)
Environment scope: *
```

- **GitLab URL** is the instance URL without `/api/v4`. Include the installation subpath if applicable, for example `https://example.com/gitlab`.
- **Group** accepts a numeric ID or the full, unescaped group path.
- **Access token** needs `api` scope and Owner access to the group for variable management. Pika encrypts this token at rest; initialize and unlock the server's encryption key before saving the resource.
- **Environment scope** selects one exact GitLab scope. The default `*` means variables whose scope is literally `*`, not every scope. Use a separate resource for `production`, `review/*`, or another scope. GitLab's environment-scope feature availability depends on the instance's tier.
- The standard outbound proxy options also apply.

Use **Test connection**, then select the resource to list its variables. Select a key, edit the value, and save. New entries use the variable name as their path, containing letters, digits, or underscores (up to 255 characters). Creating and deleting variables are also supported.

Value updates preserve the variable's existing masked, protected, raw, and variable-type settings. A file variable is edited as its content. GitLab's validation rules still apply, including masked-value constraints. New variables use GitLab's defaults for these settings.

Only the configured group's own variables are listed; parent-group and project variables are separate. Unavailable/hidden values produce a read error rather than appearing as empty editable strings. GitLab does not expose version history through this provider.

Writes check the exact scope before choosing creation or update. This check and the update are separate GitLab requests, not an atomic transaction. Avoid deleting the same variable concurrently: some GitLab versions have an unscoped fallback when a variable disappears before an update.

## Inheritance

The path is the variable key. Reads return `{"value": "..."}`. For a variable containing structured configuration, select its format to decode it before merging:

```json
{
  "resource": "gitlab-team",
  "path": "APP_CONFIG",
  "format": "yaml",
  "inject": "settings"
}
```

See [Inheritance](./) for the meaning of `paths`, `format`, and `inject`.
