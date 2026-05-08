# CloudFormation Deploy

This repo includes a single-instance CloudFormation deployment for `ptxt-nstr`:

- template: `deploy/cloudformation/ptxt-nstr-single-instance.yaml`
- artifact templates: `deploy/artifact/`
- helper scripts: `scripts/build-cfn-artifact.sh`, `scripts/upload-cfn-artifact.sh`, `scripts/reapply-cfn-artifact.sh`, `scripts/deploy-cfn.sh`
- production wrappers: `scripts/deploy-prod.sh`, `scripts/deploy-prod-infra.sh`

## What the stack creates

- one Amazon Linux 2023 ARM64 EC2 instance
- one security group allowing `80/443`
- one instance role with SSM, CloudWatch Agent, and S3 artifact read access
- one Elastic IP
- one Route 53 `A` record for your configured domain
- CloudWatch alarms for CPU credits, CPU, memory, disk, and status checks

## Artifact format

The deployment artifact is a versioned tarball containing:

- `ptxt-nstr` Linux ARM64 binary
- `install.sh`
- `ptxt-nstr.env.tmpl`
- `ptxt-nstr.service.tmpl`
- `Caddyfile.tmpl`
- `caddy.service.tmpl`
- `amazon-cloudwatch-agent.json.tmpl`

Build it locally:

```sh
./scripts/build-cfn-artifact.sh
```

Build and upload it to S3:

```sh
./scripts/upload-cfn-artifact.sh --bucket your-artifact-bucket
```

If the bucket has versioning enabled, the upload script also prints `ArtifactVersion=...` for immutable rollouts.

## Production deploys

For this repo's production environment, the default app-only deploy is:

```sh
make deploy
```

That wrapper script:

1. builds a fresh Linux ARM64 artifact
2. uploads it to the production artifact bucket
3. reapplies the artifact over SSM on the live instance

The production deploy values live in a local env file:

- `deploy/environments/prod.env`

That file is sourced by `scripts/deploy-prod.sh` before every deploy.
It is intentionally gitignored so your real infra details do not get committed.

A safe tracked template lives at:

- `deploy/environments/prod.env.example`

To set up a new local deploy env file:

```sh
cp deploy/environments/prod.env.example deploy/environments/prod.env
```

Then fill in your real values locally.

Use the same variable names as `deploy/environments/prod.env.example` in your local
`deploy/environments/prod.env`: stack name, domain, hosted zone ID, VPC and subnet
IDs, artifact bucket, and optional tunables. Only the ignored local file should
contain real environment values.

If you need to roll out infra changes too, use:

```sh
make deploy-infra
```

That wrapper:

1. builds and uploads a fresh artifact
2. updates the `ptxt-nstr-prod` CloudFormation stack
3. reapplies the artifact over SSM on the live instance

You can override the env file path or any specific value if you need to:

```sh
SUBNET_ID=subnet-1234567890abcdef0 make deploy-infra
DEPLOY_ENV_FILE=deploy/environments/prod.env make deploy
```

## Infra deploy

You need:

- an existing Route 53 hosted zone for your domain
- a public subnet in a VPC
- AWS CLI credentials with CloudFormation, EC2, IAM, Route 53, SSM, and S3 access

Example:

```sh
./scripts/deploy-cfn.sh \
  --stack-name ptxt-nstr-prod \
  --hosted-zone-id Z1234567890 \
  --vpc-id vpc-1234567890abcdef0 \
  --subnet-id subnet-1234567890abcdef0 \
  --artifact-bucket your-artifact-bucket \
  --artifact-key ptxt-nstr/deploy/ptxt-nstr-deploy-linux-arm64-20260426.tar.gz
```

Note: although the original plan targeted `6 GiB`, Amazon Linux 2023 currently requires an **8 GiB minimum root volume** because of the base AMI snapshot size, so the deploy template and helper script now default to `8`.

The infra deploy script:

1. runs `aws cloudformation deploy`
2. calls `scripts/reapply-cfn-artifact.sh`
3. uses SSM to re-run the install flow on the instance so artifact updates apply cleanly

The stack grants the instance read access to the configured artifact prefix
rather than a single object key, so app-only deploys can fetch newly uploaded
artifacts without requiring a CloudFormation update first.

## App-only deploy

For binary-only updates on an existing stack, use:

```sh
./scripts/reapply-cfn-artifact.sh \
  --stack-name ptxt-nstr-prod \
  --domain-name example.com \
  --artifact-bucket your-artifact-bucket \
  --artifact-key ptxt-nstr/deploy/ptxt-nstr-deploy-linux-arm64-20260426.tar.gz
```

That script:

1. fetches the instance ID from stack outputs
2. waits for SSM connectivity
3. downloads the artifact from S3 on the instance
4. reruns `install.sh` and restarts the services

## Data model

SQLite lives on the root EBS volume at `/var/lib/ptxt-nstr/ptxt-nstr.sqlite`.

That means:

- the first version is simple and cheap
- CloudFormation replacement can discard local SQLite state
- this is acceptable only while SQLite is treated as a cache-like local store

For production latency stability, treat local SQLite as warm cache state that
should survive routine app deploys. The next infrastructure step is to attach a
dedicated EBS data volume for `/var/lib/ptxt-nstr` (or equivalent persistent
storage) so cache warmup work survives instance replacement.
