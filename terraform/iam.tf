locals {
  firestore_services = ["user", "pipeline", "activity", "destination", "api-client", "api-webhook"]
  pubsub_publishers  = ["api-webhook", "pipeline", "activity", "api-client", "destination"]
  secret_accessors   = ["api-client", "api-public", "user", "pipeline", "activity", "destination", "api-webhook"]
  storage_services   = ["activity", "pipeline", "destination"]
}

resource "google_project_iam_member" "cr_firestore_user" {
  for_each = toset(local.firestore_services)
  project  = var.project_id
  role     = "roles/datastore.user"
  member   = "serviceAccount:${google_service_account.cloud_run_sa[each.key].email}"
}

resource "google_project_iam_member" "cr_pubsub_publisher" {
  for_each = toset(local.pubsub_publishers)
  project  = var.project_id
  role     = "roles/pubsub.publisher"
  member   = "serviceAccount:${google_service_account.cloud_run_sa[each.key].email}"
}

resource "google_project_iam_member" "cr_secret_accessor" {
  for_each = toset(local.secret_accessors)
  project  = var.project_id
  role     = "roles/secretmanager.secretAccessor"
  member   = "serviceAccount:${google_service_account.cloud_run_sa[each.key].email}"
}

resource "google_project_iam_member" "cr_storage_admin" {
  for_each = toset(local.storage_services)
  project  = var.project_id
  role     = "roles/storage.objectAdmin"
  member   = "serviceAccount:${google_service_account.cloud_run_sa[each.key].email}"
}

# AI Platform User - allows enricher to call Vertex AI Imagen API
resource "google_project_iam_member" "cr_aiplatform_user" {
  project = var.project_id
  role    = "roles/aiplatform.user"
  member  = "serviceAccount:${google_service_account.cloud_run_sa["pipeline"].email}"
}

# Service Account Token Creator - allows getSignedUrl() for GCS objects
resource "google_project_iam_member" "cr_token_creator" {
  project = var.project_id
  role    = "roles/iam.serviceAccountTokenCreator"
  member  = "serviceAccount:${google_service_account.cloud_run_sa["activity"].email}"
}

resource "google_project_iam_member" "web_deployer_run_viewer" {
  project = var.project_id
  role    = "roles/run.viewer"
  member  = "serviceAccount:circleci-web-deployer@${var.project_id}.iam.gserviceaccount.com"
}

# Service Usage Consumer - allows Firebase CLI to check API enablement status during deploy
resource "google_project_iam_member" "web_deployer_service_usage_consumer" {
  project = var.project_id
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "serviceAccount:circleci-web-deployer@${var.project_id}.iam.gserviceaccount.com"
}

# Firebase Rules Admin - allows Firebase CLI to validate and deploy Firestore rules
resource "google_project_iam_member" "web_deployer_firebase_rules_admin" {
  project = var.project_id
  role    = "roles/firebaserules.admin"
  member  = "serviceAccount:circleci-web-deployer@${var.project_id}.iam.gserviceaccount.com"
}

# Pub/Sub service agent needs publisher and subscriber rights to operate dead-letter
# forwarding. Using project-level bindings to match the deployer's IAM scope
# (resource-level pubsub IAM requires pubsub.admin which the deployer doesn't have).
resource "google_project_iam_member" "pubsub_sa_publisher" {
  project = var.project_id
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:service-${data.google_project.project.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_project_iam_member" "pubsub_sa_subscriber" {
  project = var.project_id
  role    = "roles/pubsub.subscriber"
  member  = "serviceAccount:service-${data.google_project.project.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

# ──────────────────────────────────────────────────────────────────────────────
# fitglue-box — the one identity the FitGlue services use when they run on the
# Jarvis box (personal-fitglue.md F3.2). Its key lives at
# ~/.jarvis/fitglue-box-sa.json on the box (chmod 600), never in state or CI.
#
# Created by hand with gcloud on 2026-10-25 so F3.1/F3.5 could start before
# this merged; the import block adopts it into state on the first apply
# (same pattern as fitglue-api-token in secrets.tf). The role bindings below
# were also granted by hand; google_*_iam_member is non-authoritative, so the
# apply converges on them without an import. Remove the import block in the
# PR after this one has applied.
#
# Roles, and why:
#   datastore.user            Firestore read/write (all six services)
#   pubsub.publisher          publish to the pipeline/destination/trigger topics
#   pubsub.subscriber         ack/pull on subscriptions (F3.4 timers, fitgluectl)
#   aiplatform.user           ai_banner still calls Vertex AI Imagen with ADC
#                             (only the prompt step uses GEMINI_API_KEY); the
#                             cr-pipeline-sa holds this today for the same reason
#   storage.objectAdmin       per bucket, artifacts + showcase-assets only
#   run.invoker on pipeline   what parkrun-agent has, so the parkrun agent can
#                             switch to this key in F3.4(e); goes with
#                             cloud_run.tf in F4
# Not granted: secretmanager.secretAccessor (secrets become ~/.jarvis/fitglue.env
# in F3.5), iam.serviceAccountTokenCreator (signed GCS URLs are signed locally
# with the key's private key; Cloud Run needed the IAM signBlob API instead).
# Pub/Sub push to the box keeps signing its OIDC tokens with the cr-*-sa
# accounts named in pubsub.tf; nothing here is involved in that.
# ──────────────────────────────────────────────────────────────────────────────

import {
  to = google_service_account.fitglue_box
  id = "projects/fitglue-server-prod/serviceAccounts/fitglue-box@fitglue-server-prod.iam.gserviceaccount.com"
}

resource "google_service_account" "fitglue_box" {
  account_id   = "fitglue-box"
  display_name = "FitGlue services on the Jarvis box"
}

locals {
  fitglue_box_project_roles = [
    "roles/datastore.user",
    "roles/pubsub.publisher",
    "roles/pubsub.subscriber",
    "roles/aiplatform.user",
  ]
}

resource "google_project_iam_member" "fitglue_box_project" {
  for_each = toset(local.fitglue_box_project_roles)
  project  = var.project_id
  role     = each.key
  member   = "serviceAccount:${google_service_account.fitglue_box.email}"
}

resource "google_storage_bucket_iam_member" "fitglue_box_artifacts" {
  bucket = google_storage_bucket.artifacts_bucket.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.fitglue_box.email}"
}

resource "google_storage_bucket_iam_member" "fitglue_box_showcase_assets" {
  bucket = google_storage_bucket.showcase_assets_bucket.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.fitglue_box.email}"
}

resource "google_cloud_run_v2_service_iam_member" "fitglue_box_to_pipeline" {
  name     = google_cloud_run_v2_service.backend["pipeline"].name
  location = google_cloud_run_v2_service.backend["pipeline"].location
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.fitglue_box.email}"
}
# ── end fitglue-box ───────────────────────────────────────────────────────────
