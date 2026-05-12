# AWS

Read from AWS **Secrets Manager** or **SSM Parameter Store**.

## Configuration

Under **Settings → External Resources → Add Resource → AWS**:

```text
Type      : AWS
Region    : eu-west-1
AccessKey : ...
SecretKey : ...
Service   : secretsmanager   |   ssm
```

- **Region** — AWS region the secret / parameter lives in.
- **AccessKey** / **SecretKey** — IAM credentials with read access. Use an IAM user / role scoped to only the resources pika should see.
- **Service** — pick `secretsmanager` for Secrets Manager, or `ssm` for Parameter Store.

::: tip
For least-privilege, create a dedicated IAM policy that grants `secretsmanager:GetSecretValue` (or `ssm:GetParameter` / `ssm:GetParametersByPath`) on only the ARNs pika needs.
:::

## Inheritance entry

`path` is the secret name (Secrets Manager) or the parameter name (SSM).

```json
{
  "resource": "aws",
  "path": "prod/myapp/db",
  "paths": ["password"],
  "inject": "database.password"
}
```

If the Secrets Manager secret is stored as JSON, `paths` can pick a specific field out of it.

See [Inheritance](./) for the full meaning of `paths` / `inject`.
