# GitLab Variables

Browse and edit a GitLab group's or project's CI/CD variables from **External**. Both GitLab.com and self-managed instances are supported.

## Configuration

Choose **GitLab Variables** when adding an external resource under **Settings → External Resources** (also accessible through **External → Manage**):

```text
Name             : gitlab-team
Variable source  : Group
GitLab URL       : https://gitlab.com
Group ID or path : my-team/subgroup
Access token     : (personal or group access token)
Environment scope: *
```

- **GitLab URL** is the instance URL without `/api/v4`. Include the installation subpath if applicable, for example `https://example.com/gitlab`.
- **Variable source** selects Group or Project. Existing resources continue to use their configured group.
- **Group / Project** accepts a numeric ID or the full, unescaped path. For a project, choose Project and enter `my-team/my-project` or its numeric ID. The API configuration accepts exactly one of `group` or `project`.
- **Access token** needs `api` scope, with Owner access for group variables or Maintainer/Owner access for project variables. Personal, group, or project tokens can be used where their permissions allow it. Pika encrypts this token at rest; initialize and unlock the server's encryption key before saving the resource.
- **Environment scope** selects one exact GitLab scope. The default `*` means variables whose scope is literally `*`, not every scope. Use a separate resource for `production`, `review/*`, or another scope. GitLab's environment-scope feature availability depends on the instance's tier.
- The standard outbound proxy options also apply.

Use **Test connection**, then select the resource to list its variables. Select a key, edit the value, and save. New entries use the variable name as their path, containing letters, digits, or underscores (up to 255 characters). Creating and deleting variables are also supported.

The viewer shows these settings, and both Edit and New entry let you change them:

| Setting | Meaning |
| --- | --- |
| Masked | Masks the value in CI job logs. GitLab enforces its masked-value constraints (including at least 8 characters and no spaces or line breaks). |
| Protected | Limits the variable to protected branches and tags. |
| Expand variable reference | Resolves references such as `$OTHER_VARIABLE`. This is the inverse of GitLab's `raw` flag. |
| Variable type | Variable (`env_var`) or File (`file`). For File, the value editor holds the file content. |

New entries default to an ordinary, unmasked, unprotected Variable with expansion disabled. Existing entries load their current settings. Value-only API writes still preserve any omitted settings. GitLab validates saves, including any additional masked-value restrictions when expansion is enabled. The existing hidden status is displayed read-only; changing it is not supported.

Only the configured group's or project's own variables are listed. Inherited variables are not included; configure the parent group separately to edit those. Unavailable/hidden values produce a read error rather than appearing as empty editable strings. GitLab does not expose version history through this provider. Variable settings are returned as entry metadata, separate from the value used by inheritance.

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
