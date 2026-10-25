output "fitglue_tech_name_servers" {
  description = "Name servers for the environment's zone. Configure these at your registrar (for Prod) or in the Prod zone's NS records (for Dev/Test)."
  value       = google_dns_managed_zone.main.name_servers
}

output "base_url" {
  description = "Base URL for OAuth redirects"
  value       = local.base_url
}

output "firebase_custom_domain_dns" {
  description = "Required DNS updates for Firebase Hosting custom domain"
  value       = try(google_firebase_hosting_custom_domain.main.required_dns_updates, null)
}

output "firebase_web_config" {
  description = "Firebase Web App configuration"
  value = {
    apiKey            = data.google_firebase_web_app_config.web.api_key
    authDomain        = data.google_firebase_web_app_config.web.auth_domain
    databaseURL       = data.google_firebase_web_app_config.web.database_url
    storageBucket     = data.google_firebase_web_app_config.web.storage_bucket
    messagingSenderId = data.google_firebase_web_app_config.web.messaging_sender_id
    appId             = google_firebase_web_app.web.app_id
    measurementId     = data.google_firebase_web_app_config.web.measurement_id
  }
}

output "fitglue_box_sa_email" {
  description = "Service account the FitGlue services use on the Jarvis box (key at ~/.jarvis/fitglue-box-sa.json)"
  value       = google_service_account.fitglue_box.email
}
