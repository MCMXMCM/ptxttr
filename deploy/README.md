# CloudFormation Deploy

This repo includes a single-instance CloudFormation deployment for `ptxt-nstr`:

- template: `deploy/cloudformation/ptxt-nstr-single-instance.yaml`
- artifact templates: `deploy/artifact/`
- helper scripts: `scripts/build-cfn-artifact.sh`, `scripts/upload-cfn-artifact.sh`, `scripts/reapply-cfn-artifact.sh`, `scripts/deploy-cfn.sh`, `scripts/deploy-cloudfront-cfn.sh`, `scripts/caddy-traffic-snapshot.sh`, `scripts/grow-prod-volume.sh`, `scripts/grow-prod-data-volume.sh`
- edge cache template: `deploy/cloudformation/ptxt-nstr-cloudfront.yaml`
- production wrappers: `scripts/deploy-prod.sh`, `scripts/deploy-prod-infra.sh`, `scripts/deploy-prod-cloudfront.sh`

## What the stack creates

- one Amazon Linux 2023 ARM64 EC2 instance
- one security group allowing `80/443`
- one instance role with SSM, CloudWatch Agent, S3 artifact read access, and scoped `PutObject` to the deployment artifact bucket under `…/diagnostics/*` for incident artifacts
- one Elastic IP
- one Route 53 `A` record for your configured domain
- CloudWatch alarms for CPU credits, CPU, memory, **root and data** disk usage, and status checks (disk metrics use `InstanceId` and `path`; after pulling template changes that adjust aggregation or paths, run `make deploy-infra` once so alarms and the agent config on the instance stay in sync)

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

**Prerequisite:** `make deploy` and [`scripts/reapply-cfn-artifact.sh`](../scripts/reapply-cfn-artifact.sh) read the stack output `DataVolumeId` and pass it to `install.sh`. If your CloudFormation stack predates the dedicated data volume template, **run `make deploy-infra` once** (or otherwise deploy the updated single-instance template) before relying on app-only deploys, or those steps will fail until `DataVolumeId` exists. See [Data model](#data-model) (first cutover and mount flow).

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

To sanity-check templates locally (requires AWS credentials for `cloudformation:ValidateTemplate`), run `make validate-cfn` from the repo root. You can also run [`cfn-lint`](https://github.com/aws-cloudformation/cfn-lint) on the YAML under `deploy/cloudformation/` if you install it (`pip install cfn-lint`).

The stack grants the instance read access to the configured artifact prefix
rather than a single object key, so app-only deploys can fetch newly uploaded
artifacts without requiring a CloudFormation update first.

## Traffic snapshot (compare before/after Caddy or bot policy changes)

Logs Insights hourly counts plus optional EC2 `CPUCreditBalance`:

```sh
LOG_GROUP=/ptxt-nstr/example.com/caddy-access \
INSTANCE_ID=i-0123456789abcdef0 \
./scripts/caddy-traffic-snapshot.sh
```

Set only `LOG_GROUP` if you do not need CPU metrics. Run once before and once after deploying a new artifact so **total_requests** and credits trends are comparable.

## CloudFront edge cache

Template: [`deploy/cloudformation/ptxt-nstr-cloudfront.yaml`](cloudformation/ptxt-nstr-cloudfront.yaml). Always deploy this stack in **us-east-1** (CloudFront requires its ACM certificate in that region; the stack creates and DNS-validates the cert in-place against the supplied hosted zone).

### What the stack provisions

- ACM certificate for **`ViewerDomainName`**, validated automatically.
- A CloudFront distribution with `PriceClass_100` (NA + EU edges) by default.
- A **CloudFront Function** (`cloudfront-js-2.0`) on every cache behavior that copies the viewer `Host` header into `X-Forwarded-Host`. Required because CloudFront rewrites `Host` to `OriginDomainName` for SNI to the origin, and the Go app keys canonical / OpenGraph URLs off `X-Forwarded-Host` (see [`internal/httpx/opengraph.go`](../internal/httpx/opengraph.go)). The function deliberately does **not** set `X-Forwarded-Proto` because that header is on CloudFront disallowed-modify list; Caddy to app is HTTPS so `r.TLS != nil` and the Go fallback already produces `https`.
- Default behavior: `CachingDisabled` so `/`, `/feed`, `/api/*` (except `/api/event/*`), `/login`, `POST` etc. pass through unmodified.
- Cached behaviors:
  - Static / image surfaces (`/static/*`, `/avatar/*`) use the managed `CachingOptimized` policy (query strings stripped from the cache key — safe because these paths have no meaningful query-string variants).
  - OpenGraph images (`/og/*`) use `CachingOptimized` and the origin emits a 24-hour `s-maxage` (`cacheControlContentAddressedLong` in [`internal/httpx/cache_headers.go`](../internal/httpx/cache_headers.go)) — the rendering is fully derived from an immutable event, so a long edge TTL is safe.
  - HTML surfaces (`/thread*`, `/u/*`, `/e/*`) use the managed `UseOriginCacheControlHeaders-QueryStrings` policy. Query strings are **part of the cache key** so HTMX partial responses (e.g. `?fragment=summary`) do not collide with the full-page response under the same path. The policy also honors origin `Cache-Control`, so the fragment responses (which don't set `Cache-Control`) bypass the edge cache while the full-page responses get `s-maxage=300` from [`internal/httpx/cache_headers.go`](../internal/httpx/cache_headers.go). The 5-minute window bounds how long an anonymous viewer can see a thread/user shell that is missing a brand-new reply or post before CloudFront revalidates with the origin (which has already indexed the new event). We deliberately do **not** wire CloudFront invalidations into the publish path: at current volume the per-path invalidation cost exceeds the cache savings, and a 5-minute staleness ceiling is acceptable for anonymous SSR.
  - **`/api/event/*`** uses `CachingOptimized` (id is the entire cache key) and the origin emits `Cache-Control: public, max-age=31536000, s-maxage=31536000, immutable`. Nostr events are content-addressed and signed; once an id resolves it never changes, so an indefinite edge TTL is the right shape for hydration-style client fetches.

Viewer identity and per-viewer view preferences travel in request headers (set by [`web/static/js/session.js`](../web/static/js/session.js)'s `sessionHeaders()`/`fetchWithSession()` on every fetch and SPA hydration), so the SSR response shape no longer encodes who is logged in or what their UI prefs look like. The eight headers are:

  | Header | localStorage source | Legacy query-string fallback |
  |---|---|---|
  | `X-Ptxt-Viewer` | `ptxt_nostr_session.pubkey` | `?pubkey=` |
  | `X-Ptxt-Wot-Seed` | `ptxt_wot_seed_pubkey` | `?seed_pubkey=` |
  | `X-Ptxt-Relays` | `ptxt_relays` (joined `,`) | `?relays=` / `?relay=` |
  | `X-Ptxt-Sort` | `ptxt_feed_sort` / `ptxt_reads_sort` (per path) | `?sort=` |
  | `X-Ptxt-Tf` | `ptxt_trending_tf` | `?tf=` |
  | `X-Ptxt-Reads-Tf` | `ptxt_reads_trending_tf` | `?reads_tf=` |
  | `X-Ptxt-Wot` | `ptxt_wot_enabled` | `?wot=` |
  | `X-Ptxt-Wot-Depth` | `ptxt_wot_depth` | `?wot_depth=` |

  The server reads each value via the header-preferring helpers in [`internal/httpx/viewer_request.go`](../internal/httpx/viewer_request.go), falling back to the legacy query strings only when the header is absent. That keeps old bookmarks, external `/thread/<id>?wot=1&relays=…` links, and crawlers working unchanged while letting freshly-built links omit the params.

  Two consequences for cache shape:

  - `/thread/<id>` and `/u/<id>` cache keys now collapse to **path + `?fragment=…`** regardless of viewer prefs. CloudFront's `UseOriginCacheControlHeaders-QueryStrings` policy still keeps `?fragment=summary` separate from the full-page response, but viewer pref params no longer fragment the cache.
  - Feed-like routes (`/`, `/feed`, `/reads`, `/notifications`, `/bookmarks`) remain on the default `CachingDisabled` behavior. Caching them at the edge would require wiring the new `X-Ptxt-*` headers into a `CachePolicy` `HeadersConfig`, which is deferred to a follow-up — the win here is the simpler cache key on the thread/user surfaces that already cache.

### Parameters and rollout

- **`ViewerDomainName`** — public hostname (e.g. `example.com`).
- **`OriginDomainName`** — dedicated origin hostname (e.g. `origin.example.com`). The single-instance stack owns the A record at the EIP when `OriginDomainName` is non-empty; Caddy obtains a Let's Encrypt cert for it via the multi-host site block rendered by [`install.sh`](artifact/install.sh).
- **`HostedZoneId`** — Route 53 hosted zone for DNS-validated ACM (must contain `ViewerDomainName`).
- **`PriceClass`** — defaults to `PriceClass_100` (NA + EU edges).

The CloudFront stack does not alias viewer DNS at the distribution; that swap is owned by the single-instance stack via the `ViewerDnsMode` parameter so the rollout and rollback are pure CloudFormation operations.

The single-instance template owns viewer-DNS routing via the **`ViewerDnsMode`** parameter (`DirectToEip` or `CloudFrontAlias`), so the entire rollout is parameter flips:

1. Set `ORIGIN_DOMAIN_NAME` in `deploy/environments/prod.env` and run `make deploy-infra`. This creates the origin Route 53 record, rebuilds Caddy with the multi-host site block, and waits for Let's Encrypt issuance for the origin hostname.
2. Run `make deploy-cloudfront` (runs [`scripts/deploy-prod-cloudfront.sh`](../scripts/deploy-prod-cloudfront.sh), which sources `prod.env` and invokes [`scripts/deploy-cloudfront-cfn.sh`](../scripts/deploy-cloudfront-cfn.sh)). This creates or updates the CloudFront stack (ACM cert, distribution, function). Note the output `DistributionDomainName`.
3. Set `VIEWER_DNS_MODE=CloudFrontAlias` and `CLOUDFRONT_DISTRIBUTION_DOMAIN_NAME=<distribution.cloudfront.net>` in `prod.env`, then re-run `make deploy-infra`. CloudFormation UPSERTs the apex `A` from EIP-direct to a CloudFront alias and adds the matching `AAAA`.

The single `DnsRecord` resource flips between `ResourceRecords`/`TTL` (EIP mode) and `AliasTarget` (CloudFront mode) via `Fn::If`, so the swap is one Route 53 UPSERT and avoids the create-before-delete collision two separate resources hit.

### Caddy multi-host

[`Caddyfile.tmpl`](artifact/Caddyfile.tmpl) serves TLS for both `DomainName` and `OriginDomainName` (rendered via the `__ORIGIN_DOMAIN_NAME_SUFFIX__` template variable). It also contains an explicit `header_up X-Forwarded-Host` directive guarded by a `header X-Forwarded-Host *` matcher: Caddy default behavior is to overwrite `X-Forwarded-Host` with the inbound `Host` (which is always the origin hostname when traffic comes via CloudFront), so we preserve any upstream-supplied value verbatim.

### Validation

```sh
curl -sSI https://example.com/                       # via: ...cloudfront.net
curl -sS  https://example.com/ | grep og:url         # og:url uses the viewer hostname
for i in 1 2; do curl -sSI https://example.com/u/<id> | grep x-cache; done   # Miss then Hit
for i in 1 2; do curl -sSI https://example.com/feed | grep x-cache; done     # Miss then Miss
```

### Rollback

To return traffic to the EIP-direct path, set `VIEWER_DNS_MODE=DirectToEip` and `CLOUDFRONT_DISTRIBUTION_DOMAIN_NAME=` in `prod.env` and re-run `make deploy-infra`. The CloudFront stack can remain running (cost is negligible at current load) until you have confidence in the new setup, or be deleted with `aws cloudformation delete-stack --region us-east-1 --stack-name ptxt-nstr-cloudfront-prod`.

### Optional follow-up: origin lock-down

`OriginDomainName` stays publicly reachable, so a bot can hit Caddy directly and bypass the CDN. Future hardening options, neither required for caching to work:

- Add a Caddy matcher requiring a shared-secret header that CloudFront injects via origin custom headers (the secret must stay private to CloudFront / your AWS account).
- Switch Caddy to `trusted_proxies cloudfront` using the [caddy-cloudfront](https://github.com/WeidiDeng/caddy-cloudfront) plugin, which auto-fetches CloudFront IP ranges. Requires building Caddy with xcaddy rather than pulling the upstream tarball in [`install.sh`](artifact/install.sh).

## App-only deploy

For binary-only updates on an existing stack, use (same **`DataVolumeId` stack output** prerequisite as [`make deploy`](#production-deploys); legacy stacks need an infra deploy first):

```sh
./scripts/reapply-cfn-artifact.sh \
  --stack-name ptxt-nstr-prod \
  --domain-name example.com \
  --artifact-bucket your-artifact-bucket \
  --artifact-key ptxt-nstr/deploy/ptxt-nstr-deploy-linux-arm64-20260426.tar.gz
```

That script:

1. fetches the instance ID and `DataVolumeId` from stack outputs
2. waits for SSM connectivity
3. downloads the artifact from S3 on the instance
4. reruns `install.sh` and restarts the services

After a successful artifact apply, `make deploy` also runs [`scripts/cloudfront-invalidate-static.sh`](../scripts/cloudfront-invalidate-static.sh) from [`scripts/deploy-prod.sh`](../scripts/deploy-prod.sh):

- Submits a CloudFront invalidation for `/static/*` when `CLOUDFRONT_STACK_NAME` or `CLOUDFRONT_DISTRIBUTION_ID` is set in the sourced `prod.env`, so new JS/CSS are not stuck at the edge until TTL expiry.
- Set `SKIP_CLOUDFRONT_INVALIDATION=1` to skip (e.g. local tests).
- `./scripts/reapply-cfn-artifact.sh` alone does not run this step; invoke `cloudfront-invalidate-static.sh` manually if you deploy that way.

## Data model

SQLite lives at `/var/lib/ptxt-nstr/ptxt-nstr.sqlite` on a dedicated EBS data volume mounted at `/var/lib/ptxt-nstr`. The volume is created by the single-instance stack as a separate `AWS::EC2::Volume` with both `DeletionPolicy: Retain` and `UpdateReplacePolicy: Retain`, so it survives:

- routine `make deploy-infra` runs (no-op or in-place stack updates),
- instance replacement triggered by `InstanceType`, `ImageId`, or `SubnetId` changes — the new instance reattaches the existing volume and [`deploy/artifact/install.sh`](artifact/install.sh) mounts the existing XFS filesystem at the same path,
- `aws cloudformation delete-stack` — the volume is left behind, and the next deploy can attach it back by passing `EXISTING_DATA_VOLUME_ID=vol-…` in your env file (`deploy/environments/prod.env`, copied from `prod.env.example`).

The root volume is still ephemeral (`DeleteOnTermination: true`), but nothing app-level lives on it anymore.

Configure the volume via the new env vars (see [`deploy/environments/prod.env.example`](environments/prod.env.example)):

- `DATA_VOLUME_SIZE_GIB` — size in GiB of the data volume (default `20`, minimum `8`).
- `DATA_VOLUME_TYPE` — `gp3` (default) or `gp2`.
- `EXISTING_DATA_VOLUME_ID` — optional; pass a retained volume ID to attach it instead of creating a new one (recovery path after `delete-stack`).
- `AVAILABILITY_ZONE` — optional; defaults to whatever AZ `SUBNET_ID` resolves to via `aws ec2 describe-subnets`.

How the mount happens at instance boot or SSM reapply:

1. CFN creates `DataVolumeAttachment` with `Device: /dev/sdf` against `AppInstance`.
2. UserData (or `scripts/reapply-cfn-artifact.sh` over SSM) calls `install.sh --data-volume-id <vol-id>`.
3. `install.sh` resolves the matching NVMe device by serial (the EBS volume ID minus the dash appears as the NVMe device serial on Nitro instances), formats it with `mkfs.xfs -L ptxt-data` only when no filesystem is present, writes an idempotent `UUID=…` line to `/etc/fstab`, and mounts it at `/var/lib/ptxt-nstr`.
4. The systemd unit declares `RequiresMountsFor=/var/lib/ptxt-nstr` so `ptxt-nstr.service` will not start until the data filesystem is mounted, even after an unrelated reboot.

### First cutover from root-volume storage

The first `make deploy-infra` after this change creates the new (empty) data volume and shadows the pre-existing `/var/lib/ptxt-nstr/ptxt-nstr.sqlite` on the root volume with the new mount. SQLite content effectively resets to empty on that one deploy (the data is cache-like; it repopulates from relays). From that point on, the volume persists across all subsequent infra rollouts.

### Recovery after stack delete

If the stack is ever destroyed, find the retained volume in the EC2 console (filter by the `Name=<stack-name>-data` tag), then set `EXISTING_DATA_VOLUME_ID=vol-…` in `prod.env` before re-running `make deploy-infra`. The new stack will reattach the volume and `install.sh` will mount the pre-existing filesystem unchanged.

### Growing the data volume

After you increase `DATA_VOLUME_SIZE_GIB` (or enlarge the data volume in the EC2 console) and the stack update finishes, EBS exposes the larger block size but the XFS filesystem does not grow until you run `xfs_growfs` on the mount. Use:

```sh
make grow-prod-data-volume
```

That runs [`scripts/grow-prod-data-volume.sh`](../scripts/grow-prod-data-volume.sh), which waits for volume modification to settle, then uses SSM to run `xfs_growfs -d /var/lib/ptxt-nstr` on the instance. For the **root** volume only, use `make grow-prod-volume` after raising `ROOT_VOLUME_SIZE_GIB`.
