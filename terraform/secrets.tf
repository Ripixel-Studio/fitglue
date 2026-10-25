# Add IAM binding to allow Cloud Run (Default Compute SA) to access these secrets
data "google_project" "project" {
}

resource "google_project_iam_member" "secret_accessor" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${data.google_project.project.number}-compute@developer.gserviceaccount.com"
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

