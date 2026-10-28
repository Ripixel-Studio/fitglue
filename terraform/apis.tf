resource "google_project_service" "apis" {
  for_each = toset([
    "firestore.googleapis.com",
    "pubsub.googleapis.com",
    "cloudfunctions.googleapis.com",
    "run.googleapis.com",
    "eventarc.googleapis.com",
    "artifactregistry.googleapis.com",
    "cloudscheduler.googleapis.com",
    "secretmanager.googleapis.com",
    "cloudbuild.googleapis.com",
    "iam.googleapis.com",
    "compute.googleapis.com",
    "dns.googleapis.com",
    "eventarc.googleapis.com",
    "aiplatform.googleapis.com",
    "cloudtasks.googleapis.com",
    "sheets.googleapis.com",
    # Google Health API: heart rate, sleep, body, food (replaces the Fitbit Web API).
    "health.googleapis.com"
  ])

  project = var.project_id
  service = each.key

  disable_on_destroy = false
}
