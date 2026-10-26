# Push subscriptions deliver to the box (rethink F3.4) through Tailscale Funnel →
# Caddy (deploy/box/caddy/Caddyfile, docs/guides/box-ingress.md):
#   /fg/pubsub/pipeline/{raw,run,enriched,parkrun-check} → pipeline    8104 /pubsub/…
#   /fg/pubsub/activity/roundup                          → activity    8105 /pubsub/roundup
#   /fg/pubsub/destination/                              → destination 8106 /
# Each push carries an OIDC token signed as the per-service cr-<svc>-sa (unchanged
# from Cloud Run) with ONE explicit audience for every subscription. The services
# verify it in-process (infra.PubSubPushAuthFromEnv, F3.1) against
# PUBSUB_PUSH_AUDIENCE in ~/.jarvis/fitglue.env, which must equal
# local.box_pubsub_audience exactly. Pub/Sub's default audience is the push URL,
# which differs per subscription and is rewritten by Caddy, hence the fixed value.
locals {
  box_pubsub_base     = "https://jarvis.tail3065f7.ts.net/fg/pubsub"
  box_pubsub_audience = local.box_pubsub_base
}

resource "google_pubsub_topic" "raw_activity" {
  name    = "topic-raw-activity"
  project = var.project_id

  # Enable message retention for replay (1 hour)
  message_retention_duration = "3600s"
}

resource "google_pubsub_topic" "enriched_activity" {
  name    = "topic-enriched-activity"
  project = var.project_id

  # Enable message retention for replay (1 hour)
  message_retention_duration = "3600s"
}

# Pipeline activity topic - carries activities with specific pipelineId set
# Pipeline Splitter publishes here, Enricher subscribes
resource "google_pubsub_topic" "pipeline_activity" {
  name    = "topic-pipeline-activity"
  project = var.project_id

  # Enable message retention for replay (1 hour)
  message_retention_duration = "3600s"
}

resource "google_pubsub_topic" "destination_upload" {
  name    = "topic-destination-upload"
  project = var.project_id

  message_retention_duration = "3600s"
}

# Enrichment lag topic — receives activities that need to wait for device sync
# (e.g. Fitbit HR data not yet available). The enricher publishes here on first
# retry; this subscription pushes back to /pubsub/run with isLagRetry=true.
resource "google_pubsub_topic" "enrichment_lag" {
  name    = "topic-enrichment-lag"
  project = var.project_id

  # Retain for 24h — must outlive the longest retry window a provider can
  # request (e.g. an hour-scale Retry-After from a rate-limited calendar feed)
  # plus the lag exhaustion window and the redelivery backoff on top.
  message_retention_duration = "86400s"
}

# Dead-letter topic for pipeline-run messages that exhaust all retries.
# Catches any error type that slips past the TerminalError / RetryableError handling.
resource "google_pubsub_topic" "pipeline_run_dead_letter" {
  name    = "topic-pipeline-run-dead-letter"
  project = var.project_id

  message_retention_duration = "604800s" # 7 days for investigation
}

# Dead-letter topic for lag messages that exhaust all retries.
resource "google_pubsub_topic" "enrichment_lag_dead_letter" {
  name    = "topic-enrichment-lag-dead-letter"
  project = var.project_id

  message_retention_duration = "604800s" # 7 days for investigation
}

resource "google_pubsub_subscription" "enrichment_lag_sub" {
  name    = "sub-enrichment-lag"
  topic   = google_pubsub_topic.enrichment_lag.name
  project = var.project_id

  push_config {
    push_endpoint = "${local.box_pubsub_base}/pipeline/run"
    oidc_token {
      service_account_email = google_service_account.cloud_run_sa["pipeline"].email
      audience              = local.box_pubsub_audience
    }
  }

  ack_deadline_seconds = 600

  # Retain for 24h, aligned to the topic retention.
  message_retention_duration = "86400s"

  # Max backoff (Pub/Sub caps at 600s) — a NACKed lag message that is waiting out
  # a provider-requested retry window (Retry-After) polls at most every 10 min.
  retry_policy {
    minimum_backoff = "60s"
    maximum_backoff = "600s"
  }

  # 100 attempts (the Pub/Sub maximum) at up to 600s apart gives roughly a day of
  # retry budget. 10 attempts at ≤300s was exhausted in ~30 min — entirely inside
  # an hour-scale rate-limit window, so rate-limited enrichments always died.
  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.enrichment_lag_dead_letter.id
    max_delivery_attempts = 100
  }
}

# Retention subscriptions for the dead-letter topics. Pub/Sub silently discards
# messages published to a topic with no subscription, so without these an
# exhausted retry vanished without a trace. These are pull subscriptions with no
# live consumer — they exist so dead-lettered messages are retained for
# investigation (gcloud pubsub subscriptions pull) and so the DLQ alert has a
# metric to fire on.
resource "google_pubsub_subscription" "pipeline_run_dead_letter_sub" {
  name    = "sub-pipeline-run-dead-letter"
  topic   = google_pubsub_topic.pipeline_run_dead_letter.name
  project = var.project_id

  ack_deadline_seconds       = 60
  message_retention_duration = "604800s" # 7 days for investigation

  # Never auto-delete despite having no active pull consumers.
  expiration_policy {
    ttl = ""
  }
}

resource "google_pubsub_subscription" "enrichment_lag_dead_letter_sub" {
  name    = "sub-enrichment-lag-dead-letter"
  topic   = google_pubsub_topic.enrichment_lag_dead_letter.name
  project = var.project_id

  ack_deadline_seconds       = 60
  message_retention_duration = "604800s" # 7 days for investigation

  expiration_policy {
    ttl = ""
  }
}


resource "google_pubsub_topic" "parkrun_results_trigger" {
  name    = "topic-parkrun-results-trigger"
  project = var.project_id

  # Trigger messages are only useful until the next scheduled check (2h).
  # Short retention prevents stale triggers stacking up if the handler is down.
  message_retention_duration = "7200s"
}

resource "google_pubsub_subscription" "parkrun_results_check_sub" {
  name    = "sub-parkrun-results-check"
  topic   = google_pubsub_topic.parkrun_results_trigger.name
  project = var.project_id

  push_config {
    push_endpoint = "${local.box_pubsub_base}/pipeline/parkrun-check"
    oidc_token {
      service_account_email = google_service_account.cloud_run_sa["pipeline"].email
      audience              = local.box_pubsub_audience
    }
  }

  # The checker scans all waiting parkrun pending inputs — give it 5 min.
  ack_deadline_seconds = 300

  # Discard undelivered messages after 2h (aligned to check interval).
  # If the handler is down that long, the next timer fire will start fresh.
  message_retention_duration = "7200s"

  retry_policy {
    minimum_backoff = "30s"
    maximum_backoff = "300s"
  }
}

resource "google_pubsub_subscription" "destination_upload_sub" {
  name  = "sub-destination-upload"
  topic = google_pubsub_topic.destination_upload.name

  push_config {
    push_endpoint = "${local.box_pubsub_base}/destination/"
    oidc_token {
      service_account_email = google_service_account.cloud_run_sa["destination"].email
      audience              = local.box_pubsub_audience
    }
  }

  ack_deadline_seconds       = 600
  message_retention_duration = "3600s"

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }
}

resource "google_pubsub_subscription" "pipeline_raw_sub" {
  name  = "sub-pipeline-raw"
  topic = google_pubsub_topic.raw_activity.name

  push_config {
    push_endpoint = "${local.box_pubsub_base}/pipeline/raw"
    oidc_token {
      service_account_email = google_service_account.cloud_run_sa["pipeline"].email
      audience              = local.box_pubsub_audience
    }
  }

  ack_deadline_seconds = 600
  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }
}

resource "google_pubsub_subscription" "pipeline_enriched_sub" {
  name  = "sub-pipeline-enriched"
  topic = google_pubsub_topic.enriched_activity.name

  push_config {
    push_endpoint = "${local.box_pubsub_base}/pipeline/enriched"
    oidc_token {
      service_account_email = google_service_account.cloud_run_sa["pipeline"].email
      audience              = local.box_pubsub_audience
    }
  }

  ack_deadline_seconds = 600
  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }
}

resource "google_pubsub_subscription" "pipeline_run_sub" {
  name  = "sub-pipeline-run"
  topic = google_pubsub_topic.pipeline_activity.name

  push_config {
    push_endpoint = "${local.box_pubsub_base}/pipeline/run"
    oidc_token {
      service_account_email = google_service_account.cloud_run_sa["pipeline"].email
      audience              = local.box_pubsub_audience
    }
  }

  ack_deadline_seconds = 600
  retry_policy {
    minimum_backoff = "30s"
    maximum_backoff = "600s"
  }

  # Safety net: if a message slips past TerminalError handling and keeps NACKing,
  # dead-letter it after 5 attempts rather than retrying indefinitely.
  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.pipeline_run_dead_letter.id
    max_delivery_attempts = 5
  }
}

# Roundup trigger — the box's fitglue-roundup-{weekly,monthly,yearly}.timer units
# publish here (`fitgluectl trigger roundup`, F3.4; Cloud Scheduler before that).
resource "google_pubsub_topic" "roundup_trigger" {
  name    = "topic-roundup-trigger"
  project = var.project_id

  message_retention_duration = "7200s"
}

resource "google_pubsub_subscription" "roundup_trigger_sub" {
  name    = "sub-roundup-trigger"
  topic   = google_pubsub_topic.roundup_trigger.name
  project = var.project_id

  push_config {
    push_endpoint = "${local.box_pubsub_base}/activity/roundup"
    oidc_token {
      service_account_email = google_service_account.cloud_run_sa["activity"].email
      audience              = local.box_pubsub_audience
    }
  }

  ack_deadline_seconds       = 600
  message_retention_duration = "7200s"

  retry_policy {
    minimum_backoff = "60s"
    maximum_backoff = "600s"
  }
}
