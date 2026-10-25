# RETIRED IN F4.2 — do not add secrets here.
#
# Since rethink F3.5 the services read every one of these values from
# ~/.jarvis/fitglue.env on the box (docs/guides/box-secrets.md). This file and
# the secret_key_ref blocks in cloud_run.tf stay until F4.1 destroys the Cloud
# Run services: removing the refs first would make the next `terraform apply`
# roll a revision without its secrets while Cloud Run is still the live
# deployment. F4.2 deletes this file, the refs, and these Secret Manager
# secrets (names, env key in fitglue.env):
#   fitglue-api-token            FITGLUE_API_TOKEN
#   oauth-state-secret           OAUTH_STATE_SECRET
#   gemini-api-key               GEMINI_API_KEY
#   strava-client-id             STRAVA_CLIENT_ID
#   strava-client-secret         STRAVA_CLIENT_SECRET
#   strava-verify-token          STRAVA_WEBHOOK_VERIFY_TOKEN
#   fitbit-client-id             FITBIT_CLIENT_ID
#   fitbit-client-secret         FITBIT_CLIENT_SECRET + FITBIT_OAUTH_CLIENT_SECRET
#   fitbit-verification-code     FITBIT_SUBSCRIBER_VERIFICATION_TOKEN
#   trainingpeaks-client-id      TRAININGPEAKS_CLIENT_ID      (still PLACEHOLDER_REPLACE_ME)
#   trainingpeaks-client-secret  TRAININGPEAKS_CLIENT_SECRET  (still PLACEHOLDER_REPLACE_ME)
#   github-client-id             GITHUB_CLIENT_ID
#   github-client-secret         GITHUB_CLIENT_SECRET
#   github-webhook-secret        GITHUB_WEBHOOK_SECRET
#   spotify-client-id            SPOTIFY_CLIENT_ID            (still PLACEHOLDER_REPLACE_ME)
#   spotify-client-secret        SPOTIFY_CLIENT_SECRET        (still PLACEHOLDER_REPLACE_ME)
#   google-client-id             — no reader left (Google Sheets destination is gone); not copied
#   google-client-secret         — same
# Plus the secretAccessor binding below. The fitglue-box SA never gets
# secretAccessor: the copy was a one-off under James's gcloud identity.

# Add IAM binding to allow Cloud Run (Default Compute SA) to access these secrets
data "google_project" "project" {
}

resource "google_project_iam_member" "secret_accessor" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${data.google_project.project.number}-compute@developer.gserviceaccount.com"
}

# =============================================================================
# FitGlue API token (static bearer; the only credential api-client accepts)
# =============================================================================
# The value is never known to terraform. Create the secret AND its first
# version out of band before the apply that introduces this block runs:
#
#   gcloud secrets create fitglue-api-token --replication-policy=automatic \
#     --project fitglue-server-prod
#   openssl rand -hex 32 | tr -d '\n' | gcloud secrets versions add \
#     fitglue-api-token --data-file=- --project fitglue-server-prod
#
# The import block adopts that secret into state on the first apply and is a
# no-op afterwards. No placeholder version is created here on purpose: a
# placeholder would be a working bearer token until rotated. If the secret
# does not exist the import fails and nothing in that apply is deployed.
# Rotate with another `gcloud secrets versions add`; api-client reads `latest`
# at revision start, so redeploy (or restart) after rotating.
import {
  to = google_secret_manager_secret.fitglue_api_token
  id = "projects/fitglue-server-prod/secrets/fitglue-api-token"
}

resource "google_secret_manager_secret" "fitglue_api_token" {
  secret_id = "fitglue-api-token"
  replication {
    auto {}
  }
}

# =============================================================================
# OAuth State Secret (for CSRF protection)
# =============================================================================
resource "google_secret_manager_secret" "oauth_state_secret" {
  secret_id = "oauth-state-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "oauth_state_secret_initial" {
  secret      = google_secret_manager_secret.oauth_state_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# =============================================================================
# Strava OAuth Credentials
# =============================================================================
resource "google_secret_manager_secret" "strava_client_id" {
  secret_id = "strava-client-id"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "strava_client_id_initial" {
  secret      = google_secret_manager_secret.strava_client_id.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "strava_client_secret" {
  secret_id = "strava-client-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "strava_client_secret_initial" {
  secret      = google_secret_manager_secret.strava_client_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "strava_verify_token" {
  secret_id = "strava-verify-token"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "strava_verify_token_initial" {
  secret      = google_secret_manager_secret.strava_verify_token.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# =============================================================================
# Fitbit OAuth Credentials
# =============================================================================
resource "google_secret_manager_secret" "fitbit_client_id" {
  secret_id = "fitbit-client-id"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "fitbit_client_id_initial" {
  secret      = google_secret_manager_secret.fitbit_client_id.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "fitbit_client_secret" {
  secret_id = "fitbit-client-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "fitbit_client_secret_initial" {
  secret      = google_secret_manager_secret.fitbit_client_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "fitbit_verification_code" {
  secret_id = "fitbit-verification-code"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "fitbit_verification_code_initial" {
  secret      = google_secret_manager_secret.fitbit_verification_code.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# =============================================================================
# Spotify OAuth Credentials
# =============================================================================
resource "google_secret_manager_secret" "spotify_client_id" {
  secret_id = "spotify-client-id"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "spotify_client_id_initial" {
  secret      = google_secret_manager_secret.spotify_client_id.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "spotify_client_secret" {
  secret_id = "spotify-client-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "spotify_client_secret_initial" {
  secret      = google_secret_manager_secret.spotify_client_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# =============================================================================
# TrainingPeaks OAuth Credentials
# =============================================================================
resource "google_secret_manager_secret" "trainingpeaks_client_id" {
  secret_id = "trainingpeaks-client-id"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "trainingpeaks_client_id_initial" {
  secret      = google_secret_manager_secret.trainingpeaks_client_id.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "trainingpeaks_client_secret" {
  secret_id = "trainingpeaks-client-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "trainingpeaks_client_secret_initial" {
  secret      = google_secret_manager_secret.trainingpeaks_client_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# =============================================================================
# Gemini API key for AI description enricher
# =============================================================================
resource "google_secret_manager_secret" "gemini_api_key" {
  secret_id = "gemini-api-key"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "gemini_api_key_initial" {
  secret      = google_secret_manager_secret.gemini_api_key.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# =============================================================================
# Google OAuth Credentials (for Google Sheets integration)
# =============================================================================
resource "google_secret_manager_secret" "google_client_id" {
  secret_id = "google-client-id"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "google_client_id_initial" {
  secret      = google_secret_manager_secret.google_client_id.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "google_client_secret" {
  secret_id = "google-client-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "google_client_secret_initial" {
  secret      = google_secret_manager_secret.google_client_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# =============================================================================
# GitHub OAuth Credentials & Webhook Secret
# =============================================================================
resource "google_secret_manager_secret" "github_client_id" {
  secret_id = "github-client-id"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "github_client_id_initial" {
  secret      = google_secret_manager_secret.github_client_id.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "github_client_secret" {
  secret_id = "github-client-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "github_client_secret_initial" {
  secret      = google_secret_manager_secret.github_client_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "github_webhook_secret" {
  secret_id = "github-webhook-secret"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "github_webhook_secret_initial" {
  secret      = google_secret_manager_secret.github_webhook_secret.id
  secret_data = "PLACEHOLDER_REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# Note: To update a secret value after initial creation, use:
# gcloud secrets versions add <secret-id> --data-file=- <<< "your-actual-secret-value"

