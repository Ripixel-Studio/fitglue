# Terraform Infrastructure

FitGlue's infrastructure is managed as code using Terraform. This document describes the workspace strategy, key resources, and deployment patterns.

## Overview

All cloud resources are defined in `/terraform/` and deployed via CI/CD.

## Workspace Strategy

FitGlue uses **Terraform Workspaces** with environment-specific variable files:

| Workspace | GCP Project | Variable File | URL |
|-----------|-------------|---------------|-----|
| `dev` | `fitglue-server-dev` | `envs/dev.tfvars` | `dev.fitglue.tech` |
| `test` | `fitglue-server-test` | `envs/test.tfvars` | `test.fitglue.tech` |
| `prod` | `fitglue-server-prod` | `envs/prod.tfvars` | `fitglue.tech` |

### Switching Workspaces

```bash
cd terraform

# List workspaces
terraform workspace list

# Switch to dev
terraform workspace select dev

# Plan with environment-specific vars
terraform plan -var-file=envs/dev.tfvars
```

## State Management

Terraform state is stored locally with workspace isolation:

```
terraform/
├── terraform.tfstate.d/
│   ├── dev/
│   │   └── terraform.tfstate
│   ├── test/
│   │   └── terraform.tfstate
│   └── prod/
│       └── terraform.tfstate
```

> [!NOTE]
> State is stored in the repository for simplicity. For larger teams, consider remote state (GCS bucket).

## Key Resources

### Cloud Run Services (`cloud_run.tf`)

All 6 services are defined as Cloud Run v2 services using a `for_each` over two local maps:
- `frontend_services`: `api-client`, `api-webhook`
- `backend_services`: `user`, `pipeline`, `activity`, `destination`

Each service gets:
- A dedicated service account (`cr-{name}-sa`)
- Docker image from Artifact Registry
- Environment-specific configuration via `var.*` variables
- Service-specific secrets from Secret Manager

```hcl
resource "google_cloud_run_v2_service" "backend" {
  for_each            = local.backend_services
  name                = each.key
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    service_account = google_service_account.cloud_run_sa[each.key].email
    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/...//${each.key}:${var.image_tag}"
      # ... env vars, secrets
    }
    scaling {
      min_instance_count = 0
      max_instance_count = 3
    }
  }
}
```

### Pub/Sub Topics (`pubsub.tf`)

Event-driven messaging between functions:

| Topic | Publisher | Subscriber |
|-------|-----------|------------|
| `topic-raw-activity` | Webhook sources | Pipeline (splitter) |
| `topic-pipeline-activity` | Pipeline (splitter) | Pipeline (enricher) |
| `topic-enriched-activity` | Pipeline (enricher) | Destination |
| `topic-destination-upload` | Pipeline (router) | Destination |
| `topic-parkrun-results-trigger` | `fitglue-parkrun-check.timer` on the box (`fitgluectl trigger parkrun-check`; Cloud Scheduler until F3.4) | Pipeline |
| `topic-roundup-trigger` | `fitglue-roundup-{weekly,monthly,yearly}.timer` on the box (`fitgluectl trigger roundup`) | Activity |

### Firestore (`firestore.tf`)

Database configuration with indexes for common queries:

```hcl
resource "google_firestore_database" "main" {
  name        = "(default)"
  location_id = var.region
  type        = "FIRESTORE_NATIVE"
}

resource "google_firestore_index" "executions_by_user" {
  collection = "executions"
  fields {
    field_path = "user_id"
    order      = "ASCENDING"
  }
  fields {
    field_path = "created_at"
    order      = "DESCENDING"
  }
}
```

### Cloud Storage (`storage.tf`)

Buckets for FIT files and artifacts:

| Bucket | Purpose | Lifecycle |
|--------|---------|-----------|
| `{project}-artifacts` | Raw payloads, FIT files, enriched events, activity records | `payloads/` pruned after 30 days; everything else kept |
| `{project}-showcase-assets` | Generated images (banners, route thumbnails, heatmaps); public read | none |
| `{project}-version-config` | Retired; non-empty and versioned, destroyed in F4.2 | none |

### Secrets (`secrets.tf`)

Secret Manager for sensitive values:

- `fitglue-api-token` — the static bearer token `api-client` accepts. Terraform
  declares the secret but never a version: create both out of band
  (`gcloud secrets create fitglue-api-token`, then
  `openssl rand -hex 32 | tr -d '\n' | gcloud secrets versions add fitglue-api-token --data-file=-`)
  before the apply that introduces it; an `import` block in `secrets.tf` adopts
  the secret into state. Rotate with another `versions add` and redeploy.
- OAuth client secrets
- Webhook signing secrets
- API keys

Plain values that are not secrets but identify the deployment live in
`envs/<env>.tfvars`: `owner_uid` is the single owner's user id and becomes
`FITGLUE_OWNER_UID` on `api-client`.

```hcl
resource "google_secret_manager_secret" "strava_client_secret" {
  secret_id = "strava-client-secret"
  replication {
    auto {}
  }
}
```

### DNS (`dns.tf`)

Cloud DNS zones for domain management:

```hcl
resource "google_dns_managed_zone" "main" {
  name        = "fitglue-zone"
  dns_name    = "${var.subdomain}.fitglue.tech."
  description = "DNS zone for ${var.environment}"
}
```

### IAM (`iam.tf`)

Service accounts and permissions:

| Service Account | Purpose | Key Roles |
|-----------------|---------|-----------|
| `circleci-deployer` | CI/CD deployment (until F4) | Editor, Run Admin, Project IAM Admin |
| `cr-<service>-sa` | One per Cloud Run service (until F4) | Datastore User, Pub/Sub Publisher, Secret Accessor, Storage Object Admin (project-level); `pipeline` also AI Platform User, `activity` also SA Token Creator |
| `parkrun-agent` | Off-cloud parkrun fetch agent on the box | Run Invoker on `pipeline` only. F3.4(e) switched the agent to the `fitglue-box` key (`~/.jarvis/parkrun-agent.env`); this SA and its grant go with Cloud Run in F4 |
| `fitglue-box` | **The FitGlue services on the Jarvis box** (F3.2) | see below |

#### `fitglue-box` — identity for the box

`google_service_account.fitglue_box` is the single identity all six services use
when they run on the box (`GOOGLE_APPLICATION_CREDENTIALS=~/.jarvis/fitglue-box-sa.json`,
written by F3.1's units). Its email is the `fitglue_box_sa_email` output:
`fitglue-box@fitglue-server-prod.iam.gserviceaccount.com`.

| Role | Scope | Why |
|------|-------|-----|
| `roles/datastore.user` | project | Firestore read/write |
| `roles/pubsub.publisher` | project | publish to the pipeline/destination/trigger topics |
| `roles/pubsub.subscriber` | project | consume subscriptions (F3.4 timers, `fitgluectl`) |
| `roles/aiplatform.user` | project | `ai_banner` calls Vertex AI Imagen with ADC; only the prompt step uses `GEMINI_API_KEY` |
| `roles/storage.objectAdmin` | `{project}-artifacts`, `{project}-showcase-assets` | per-bucket, not project-level: the version-config bucket is out of reach |
| `roles/run.invoker` | Cloud Run `pipeline` | parity with `parkrun-agent`, so the parkrun agent can switch to this key in F3.4(e); goes with `cloud_run.tf` in F4 |

Deliberately **not** granted: `secretmanager.secretAccessor` (secrets become
`~/.jarvis/fitglue.env` in F3.5), `iam.serviceAccountTokenCreator` (with a key
file the GCS client signs URLs locally with the private key; Cloud Run needed the
IAM signBlob API because it had no key), `run.viewer`/`run.admin`. Pub/Sub push
to the box keeps signing its OIDC tokens with the `cr-pipeline-sa`,
`cr-destination-sa` and `cr-activity-sa` accounts named in the `oidc_token`
blocks of `pubsub.tf`; `fitglue-box` plays no part in that path and those
accounts must outlive the Cloud Run services until F3.4 picks a replacement.

Pub/Sub bindings are project-level, like every other Pub/Sub binding in
`iam.tf`: resource-level Pub/Sub IAM needs `pubsub.admin`, which the deployer
does not hold.

**How it was created.** The account, its bindings and the key were created by
hand with James's `gcloud` on 2026-10-25 so that F3.1 and F3.5 could run before
the terraform merged. An `import` block in `iam.tf` adopts the account into state
on the first `apply`; the `google_*_iam_member` resources are non-authoritative
and converge on the hand-made bindings without an import. Once CI has applied
once, delete the `import` block: an import block for an address already in
state is a no-op, so leaving it is harmless, but it is dead config.

**Key on the box** (already done; repeat only to rotate — `keys create` makes
a new key, the old one keeps working until `keys delete`):

```bash
gcloud iam service-accounts keys create ~/.jarvis/fitglue-box-sa.json \
  --iam-account fitglue-box@fitglue-server-prod.iam.gserviceaccount.com \
  --project fitglue-server-prod
chmod 600 ~/.jarvis/fitglue-box-sa.json
```

**Verify** — the F3.2 acceptance test. Run under a throwaway `CLOUDSDK_CONFIG`
so James's own `gcloud` login on the box is not switched to the service account.
Fresh bindings take about a minute to propagate; a 403 in the first minute is
not a missing role.

```bash
export CLOUDSDK_CONFIG=$(mktemp -d)
gcloud auth activate-service-account --key-file ~/.jarvis/fitglue-box-sa.json
T=$(gcloud auth print-access-token)
# Firestore: one document from the users collection
curl -s -H "Authorization: Bearer $T" \
  "https://firestore.googleapis.com/v1/projects/fitglue-server-prod/databases/(default)/documents/users?pageSize=1&mask.fieldPaths=__name__"
# GCS: list the showcase-assets bucket
gcloud storage ls gs://fitglue-server-prod-showcase-assets --project fitglue-server-prod | head -1
# Pub/Sub: publish permission on a topic, without publishing
curl -s -X POST -H "Authorization: Bearer $T" -H "Content-Type: application/json" \
  -d '{"permissions":["pubsub.topics.publish"]}' \
  "https://pubsub.googleapis.com/v1/projects/fitglue-server-prod/topics/topic-parkrun-results-trigger:testIamPermissions"
unset CLOUDSDK_CONFIG
```

Expected: the Firestore call returns `users/EsghDvArtWhvnA8t3AkxEooUyUl1`, the
bucket listing prints a prefix, and `testIamPermissions` echoes
`pubsub.topics.publish`. (`gcloud pubsub topics list`/`describe` will 403 —
`pubsub.topics.list`/`get` are not in the publisher role, and the services never
list topics.)

## Deployment

### Manual Deployment

```bash
cd terraform

# Initialize
terraform init

# Select workspace
terraform workspace select dev

# Plan
terraform plan -var-file=envs/dev.tfvars -out=plan.tfplan

# Apply
terraform apply plan.tfplan
```

### CI/CD Deployment

Until F4 of the single-user conversion, a merge to `main` runs
`terraform apply -auto-approve -var-file=envs/prod.tfvars` from CircleCI; that
is how deletion PRs destroy their resources. Only `prod` exists. After F3 the
apply is run by hand from the box and the CircleCI pipeline is deleted.

See [CI/CD](cicd.md) for details.

## File Reference

| File | Purpose |
|------|---------|
| `main.tf` | Provider configuration |
| `variables.tf` | Variable declarations |
| `outputs.tf` | Output values |
| `versions.tf` | Provider version constraints |
| `cloud_run.tf` | All 10 Cloud Run services, service accounts, IAM |
| `pubsub.tf` | Pub/Sub topics and subscriptions |
| `firestore.tf` | Database and indexes |
| `storage.tf` | GCS buckets |
| `secrets.tf` | Secret Manager |
| `dns.tf` | Cloud DNS |
| `cdn.tf` | CDN / load balancer |
| `iam.tf` | Service accounts and bindings |
| `apis.tf` | API enablement |
| `firebase.tf` | Firebase configuration |
| `backend.tf` | Terraform backend configuration |

## Related Documentation

- [CI/CD Guide](cicd.md) - Deployment pipeline
- [Architecture Overview](../architecture/overview.md) - System components
- [ADR 002](../decisions/ADR.md#002) - Environment isolation decision
