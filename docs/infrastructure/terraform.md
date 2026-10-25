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

All 7 services are defined as Cloud Run v2 services using a `for_each` over two local maps:
- `frontend_services`: `api-client`, `api-public`, `api-webhook`
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
| `topic-parkrun-results-trigger` | Cloud Scheduler | Pipeline |

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
| `{project}-activities` | Enriched FIT files | 90 days |
| `{project}-source` | Function source code | N/A |

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
| `deployer` | CI/CD deployment | Editor, Run Admin |
| `functions` | Function execution | Pub/Sub Publisher, Storage Object Admin |

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
