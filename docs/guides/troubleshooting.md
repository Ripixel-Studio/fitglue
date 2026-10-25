# Troubleshooting Guide

This is the **primary entry point** for debugging issues in FitGlue. Whether a pipeline isn't running, an enricher is failing, or a webhook isn't arriving — start here.

> [!TIP]
> **New to FitGlue?** Read the [Architecture Overview](../architecture/overview.md) first for context on how services, Pub/Sub, and Firestore fit together.

## Quick Reference Map

Use this table to jump straight to the right service, code, and logs for any failure type.

| Failure Domain | Cloud Run Service(s) | Code Location | Cloud Logging Filter | Firestore Path |
|---|---|---|---|---|
| **Pipeline creation/editing** | `api-client` → `pipeline` | `internal/pipeline/` | `resource.labels.service_name="pipeline"` | `users/{uid}/pipelines/` |
| **Enricher failures** | `pipeline` | `internal/pipeline/providers/` | `resource.labels.service_name="pipeline"` | `users/{uid}/pipeline_runs/{id}` → `boosters[]` |
| **Webhook / source ingestion** | `api-webhook` | `services/api-webhook/internal/webhook/sources/` | `resource.labels.service_name="api-webhook"` | — |
| **Integration / connection** | `api-client` → `user` | `internal/user/` | `resource.labels.service_name="user"` | `users/{uid}` → `integrations` field |
| **Pipeline processing (split/route)** | `pipeline` | `internal/pipeline/` | `resource.labels.service_name="pipeline"` | `users/{uid}/pipeline_runs/` |
| **Destination upload** | `destination` | `services/destination/internal/destination/uploaders/` | `resource.labels.service_name="destination"` | `users/{uid}/pipeline_runs/{id}` → `destinations[]` |
| **Pending input stalls** | `pipeline` | `internal/pipeline/` | `resource.labels.service_name="pipeline"` | `users/{uid}/pending_inputs/` |

---

## 1. Pipeline Creation / Editing Failures

**Symptoms**: User gets an error creating or updating a pipeline in the web dashboard. Pipeline doesn't appear in Firestore.

### Debugging Steps

1. **Check browser console / network tab** — Look for the failing `POST /api/users/me/pipelines` or `PATCH /api/users/me/pipelines/{id}` request. Note the HTTP status code:
   - `400` → Validation error (invalid config, missing required fields)
   - `401/403` → Auth issue (expired token, wrong user)
   - `500` → Server error in `service.pipeline`

2. **Check `api-client` logs** — The API gateway translates HTTP to gRPC:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="api-client"
   severity>=WARNING
   ```

3. **Check `pipeline` service logs** — The domain service handles CRUD:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="pipeline"
   severity>=WARNING
   ```

4. **Check Firestore** — Inspect `users/{userId}/pipelines/` for the target pipeline document. Verify the schema matches `PipelineConfig` proto definition.

5. **Check Sentry** — Search [fitglue.sentry.io](https://fitglue.sentry.io/issues/) for the error, filtering by `service:pipeline` or `service:api-client`.

### Common Causes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 400 on create | Missing source or destination | Ensure at least one source and destination are configured |
| 400 on create | Invalid enricher config | Check `configSchema` in registry matches submitted config |
| 500 on create | Firestore write failure | Check service account permissions (`cr-pipeline-sa`) |
| Pipeline exists but doesn't run | Pipeline `disabled: true` | Toggle enabled in UI or patch via API |

### Key Code Paths

- **HTTP handler**: `services/api-client/internal/server/pipeline_handlers.go`
- **gRPC service**: `internal/pipeline/service.go` → `CreatePipeline()` / `UpdatePipeline()`
- **Store (Firestore)**: `internal/pipeline/store.go`
- **Validation**: `internal/pipeline/validation.go`

---

## 2. Enricher Failures

**Symptoms**: A pipeline runs but a specific booster shows `FAILED` status in the PipelineRun. Activity may arrive at destination without expected enrichment.

### Debugging Steps

1. **Check PipelineRun document** — In Firestore at `users/{userId}/pipeline_runs/{pipelineRunId}`, inspect the `boosters` array:
   ```json
   {
     "provider_name": "weather",
     "status": "FAILED",
     "error": "API rate limited",
     "duration_ms": 1200
   }
   ```

2. **Check `pipeline` service logs** — Filter by the `pipelineExecutionId`:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="pipeline"
   jsonPayload.pipeline_execution_id="<id>"
   ```

3. **Check Sentry** — Search by enricher name:
   - Tag: `enricher:<provider_name>` (e.g., `enricher:weather`)
   - The `SentryHandler` automatically captures Error-level logs with context

4. **Check enricher-specific dependencies** — Many enrichers depend on external APIs or integrations:
   - **Weather**: OpenWeatherMap API key, activity must have GPS coordinates
   - **Fitbit HR**: Valid Fitbit OAuth tokens, HR data must exist for time range
   - **Spotify Tracks**: Valid Spotify OAuth tokens
   - **Parkrun**: Activity must match location/time heuristics
   - **AI Companion/Banner**: Gemini API key

### Common Causes

| Symptom | Cause | Fix |
|---------|-------|-----|
| `FAILED` with rate limit error | External API rate limit exceeded | Retry automatically via Pub/Sub |
| `SKIPPED` status | Activity didn't match enricher's filter criteria | Expected — check filter logic in provider |
| `WAITING` status | Enricher is waiting for user input | Check `pending_inputs` collection |
| All enrichers fail | GCS artifact bucket permission issue | Check `cr-pipeline-sa` has Storage Object Admin |
| `FAILED` with nil payload | GCS offloaded data not resolved | Check `originalPayloadUri` field is valid |

### Key Code Paths

- **Enricher orchestrator**: `internal/pipeline/enricher.go`
- **Individual providers**: `internal/pipeline/providers/<name>.go`
- **Provider interface**: `internal/pipeline/providers/interfaces.go`
- **PipelineRun update**: `internal/pipeline/run_store.go` → `AddBoosterExecution()`

### Enricher Errors in `errors.md`

| Code | Retryable | Description |
|------|:---------:|-------------|
| `ENRICHER_FAILED` | ✅ | Transient failure (API error, timeout) |
| `ENRICHER_NOT_FOUND` | ❌ | Unknown enricher type in pipeline config |
| `ENRICHER_TIMEOUT` | ✅ | Enricher took too long |
| `ENRICHER_SKIPPED` | ❌ | Activity filtered by enricher logic |

---

## 3. Webhook / Source Ingestion Failures

**Symptoms**: Webhooks from external services (Strava, Hevy, Fitbit, etc.) aren't being processed. No new activities appear in Firestore.

### Debugging Steps

1. **Verify the webhook is reaching FitGlue** — Check `api-webhook` logs:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="api-webhook"
   ```
   If you see no requests at all, the webhook isn't reaching GCP (DNS, routing, or provider-side config issue).

2. **Check HMAC / signature verification** — If you see `401` responses:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="api-webhook"
   httpRequest.status=401
   ```
   The provider's webhook secret doesn't match. Check Secret Manager values.

3. **Check user resolution** — The webhook processor calls `service.user.GetIntegration()` to resolve the external user ID to a FitGlue user:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="user"
   severity>=WARNING
   ```
   If no user is found, check `integrations/{provider}/ids/{externalId}` in Firestore.

4. **Check Pub/Sub** — After successful ingestion, a message is published to `topic-raw-activity`. Check:
   - [GCP Console → Pub/Sub → Topics → topic-raw-activity](https://console.cloud.google.com/cloudpubsub/topic/detail/topic-raw-activity)
   - Verify the subscription is active and not backed up

5. **Check Sentry** — Search for webhook errors filtered by provider:
   - Tag: `source:<provider>` (e.g., `source:hevy`, `source:strava`)

### Common Causes

| Symptom | Cause | Fix |
|---------|-------|-----|
| No requests in logs | Webhook URL misconfigured at provider | Verify URL: `https://<domain>/hooks/<provider>` |
| 401 on all requests | Webhook secret mismatch | Re-configure secret in Secret Manager |
| User not found | Identity mapping missing | Check `integrations/{provider}/ids/{externalId}` exists |
| Activity fetched but not published | Source API returned empty/error | Check provider-specific API status |
| Duplicate activity skipped | Dedup check triggered | Expected — check activity already exists in Firestore |
| Mobile source (Apple Health / Health Connect) fails | JWT validation error | Check mobile auth configuration |

### Provider-Specific Webhook Endpoints

| Provider | Endpoint | Auth Method |
|----------|----------|-------------|
| Strava | `/hooks/strava` | HMAC signature |
| Hevy | `/hooks/hevy` | API key + HMAC |
| Fitbit | `/hooks/fitbit` | Subscriber verification + HMAC |
| Polar | `/hooks/polar` | OAuth signature |
| Wahoo | `/hooks/wahoo` | OAuth signature |
| Oura | `/hooks/oura` | HMAC |
| Mobile | `/hooks/mobile` | Mobile JWT |

### Key Code Paths

- **Webhook router**: `services/api-webhook/internal/server/routes.go`
- **Webhook processor**: `services/api-webhook/internal/webhook/processor.go`
- **Source providers**: `services/api-webhook/internal/webhook/sources/<provider>/provider.go`
- **User lookup (gRPC)**: `internal/user/service.go` → `GetIntegration()`

---

## 4. Integration / Connection Failures

**Symptoms**: User can't connect a new service (OAuth fails), or a connected service stops working (token expired/revoked).

### Debugging Steps

1. **OAuth connection failures** — Check the OAuth callback flow:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="api-client"
   jsonPayload.message=~"oauth"
   ```

2. **Token refresh failures** — When the destination service detects a 401 from an external API, it attempts to refresh:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="destination"
   jsonPayload.message=~"token refresh"
   ```

3. **Check user's integration state** — In Firestore at `users/{userId}`, inspect the `integrations` map:
   ```json
   {
     "integrations": {
       "strava": {
         "enabled": true,
         "access_token": "...",
         "refresh_token": "...",
         "expires_at": "2026-04-13T..."
       }
     }
   }
   ```
   If `expires_at` is in the past and the token hasn't refreshed, the refresh token may be invalid.

4. **Check identity mapping** — Verify `integrations/{provider}/ids/{externalId}` document exists and points to the correct `userId`.

5. **Check Secret Manager** — OAuth client credentials must be present:
   - `{provider}-client-id`
   - `{provider}-client-secret`
   - `oauth-state-secret` (for CSRF protection)

### Common Causes

| Symptom | Cause | Fix |
|---------|-------|-----|
| OAuth redirect fails | Callback URL mismatch | Update provider app settings to match `https://<domain>/app/connections/<provider>/success` |
| "Invalid state token" | State token expired (>10 min) | Retry the connect flow |
| Token refresh fails | User revoked access at provider | User must reconnect |
| Integration shows as disabled | Missing identity mapping | Re-authenticate to recreate mapping |

### Key Code Paths

- **OAuth flow initiation**: `services/api-client/internal/server/auth_handlers.go`
- **OAuth callback**: `services/api-client/internal/server/auth_handlers.go` → `HandleOAuthCallback()`
- **Token storage**: `internal/user/service.go` → `SetIntegration()`
- **Token refresh**: `services/destination/internal/destination/token_refresher.go`
- **Identity mapping**: `internal/user/store.go` → `CreateIdentityMapping()`

---

## 5. General Pipeline Processing Failures

**Symptoms**: Activity arrives via webhook but never completes processing. PipelineRun shows `RUNNING` or `FAILED`.

### Debugging Steps

1. **Trace the Pub/Sub message flow** — Data flows through 3 topics:
   ```
   topic-raw-activity → (splitter) → topic-pipeline-activity → (enricher) → topic-enriched-activity → (destination)
   ```

2. **Check the splitter** — Did the raw activity get split into pipeline-specific messages?
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="pipeline"
   jsonPayload.message=~"split"
   ```
   If no split happened, the user may have no active pipelines matching the source.

3. **Check PipelineRun status** — In Firestore at `users/{userId}/pipeline_runs/`, find the run:
   - `status: RUNNING` → Still processing (or stuck)
   - `status: FAILED` → Check `error` field
   - `status: WAITING` → Blocked on pending input
   - `status: TIER_BLOCKED` → User's tier doesn't allow this pipeline (ghost run)

4. **Check for Pub/Sub dead-letter** — If messages are failing repeatedly, they may be in the dead-letter topic. Check subscription metrics in GCP Console.

5. **Check GCS payloads** — Large activity payloads are offloaded to GCS. If the `originalPayloadUri` in the PipelineRun doesn't resolve:
   ```bash
   gsutil cat gs://<bucket>/<path>
   ```

### Common Causes

| Symptom | Cause | Fix |
|---------|-------|-----|
| No PipelineRun created | No matching pipeline for this source | Check user has a pipeline with the correct source type |
| PipelineRun stuck at RUNNING | Enricher or destination timeout | Check individual booster/destination statuses |
| TIER_BLOCKED status | User's tier doesn't support this pipeline | User needs to upgrade (expected behavior) |
| Activity duplicated | Repost triggered duplicate | Check for duplicate `sourceActivityId` |

### Pub/Sub Topics Reference

| Topic | Producer | Consumer | Purpose |
|-------|----------|----------|---------|
| `topic-raw-activity` | `api-webhook` | `pipeline` (splitter) | Raw ingested activities |
| `topic-mobile-activity` | `api-webhook` (mobile) | `pipeline` | Mobile health activities |
| `topic-pipeline-activity` | `pipeline` (splitter) | `pipeline` (enricher) | Per-pipeline activity messages |
| `topic-enriched-activity` | `pipeline` (enricher) | `destination` | Enriched activities for upload |
| `topic-destination-upload` | `pipeline` (router) | `destination` | Targeted upload instructions |
| `topic-parkrun-results-trigger` | Cloud Scheduler | `pipeline` | Scheduled Parkrun poll |

### Key Code Paths

- **Splitter**: `internal/pipeline/splitter.go`
- **Enricher orchestrator**: `internal/pipeline/enricher.go`
- **Router**: `internal/pipeline/router.go`
- **PipelineRun store**: `internal/pipeline/run_store.go`

---

## 6. Destination Upload Failures

**Symptoms**: Activity is enriched but doesn't arrive at the destination (Strava, TrainingPeaks, Hevy, etc.). PipelineRun shows destination with `FAILED` status.

### Debugging Steps

1. **Check PipelineRun destinations** — In Firestore at `users/{userId}/pipeline_runs/{id}`, inspect the `destinations` array:
   ```json
   {
     "destination": "STRAVA",
     "status": "FAILED",
     "error": "stream error: stream ID 5; INTERNAL_ERROR"
   }
   ```

2. **Check `destination` service logs**:
   ```
   resource.type="cloud_run_revision"
   resource.labels.service_name="destination"
   severity>=WARNING
   ```

3. **Check destination-specific errors**:
   - **Strava**: HTTP/2 stream errors are transient — will auto-retry via Pub/Sub
   - **Hevy**: Check template resolution errors (non-JSON API responses)
   - **TrainingPeaks**: OAuth token expiry
   - **Google Sheets**: Check spreadsheet permissions

4. **Check GCS for the enriched FIT file** — Uploads use FIT files from GCS:
   ```bash
   gsutil ls gs://<project-id>-artifacts/users/<userId>/
   ```

5. **Check Sentry** — Search for destination errors:
   - Tag: `destination:<name>` (e.g., `destination:strava`)

### Common Causes

| Symptom | Cause | Fix |
|---------|-------|-----|
| `INTERNAL_ERROR` stream error | Transient HTTP/2 issue | Auto-retries; if persistent, check payload size |
| Auth error (401/403) | OAuth token expired | Token refresh should happen automatically; if not, user must reconnect |
| Rate limited | Too many uploads in short period | Automatic retry with backoff |
| "Invalid character" JSON error | Destination API returned non-JSON | Check API response handling — may need response body logging |
| Template resolution failure (Hevy) | Custom exercise template creation failed | Check `templates.go` for API compatibility |
| FIT file not found in GCS | Enricher didn't produce artifact | Check enricher execution in PipelineRun |

### Key Code Paths

- **Upload executor**: `services/destination/internal/destination/executor.go`
- **Provider-specific uploaders**: `services/destination/internal/destination/uploaders/<provider>/`
  - Strava: `uploaders/strava/strava.go`
  - Hevy: `uploaders/hevy/hevy.go` + `mapper.go` + `templates.go`
  - TrainingPeaks: `uploaders/trainingpeaks/trainingpeaks.go`
  - Intervals.icu: `uploaders/intervals/intervals.go`
  - Google Sheets: `uploaders/googlesheets/googlesheets.go`
  - GitHub: `uploaders/github/github.go`
  - Showcase: `uploaders/showcase/showcase.go`
- **Token refresher**: `services/destination/internal/destination/token_refresher.go`
- **GCS payload resolver**: `services/destination/internal/destination/payload_resolver.go`

---

## 7. Logging & Monitoring Dashboard Reference

### GCP Cloud Logging

**Console**: [GCP Console → Logging → Logs Explorer](https://console.cloud.google.com/logs)

#### Useful Filter Templates

```bash
# All errors across all services
resource.type="cloud_run_revision"
severity>=ERROR

# Specific service errors
resource.type="cloud_run_revision"
resource.labels.service_name="<service>"
severity>=WARNING

# Trace a specific pipeline execution
resource.type="cloud_run_revision"
jsonPayload.pipeline_execution_id="<id>"

# Trace a specific user's activity
resource.type="cloud_run_revision"
jsonPayload.user_id="<userId>"

# Webhook requests by provider
resource.type="cloud_run_revision"
resource.labels.service_name="api-webhook"
httpRequest.requestUrl=~"/hooks/<provider>"

# Destination upload errors
resource.type="cloud_run_revision"
resource.labels.service_name="destination"
severity>=WARNING
jsonPayload.destination="<DESTINATION_NAME>"
```

**Service name values**: `api-client`, `api-public`, `api-webhook`, `user`, `pipeline`, `activity`, `destination`

### Sentry

**Console**: [fitglue.sentry.io/issues/](https://fitglue.sentry.io/issues/)

#### Useful Search Queries

| Query | Purpose |
|-------|---------|
| `is:unresolved` | All open issues |
| `service:pipeline` | Pipeline service errors |
| `service:destination` | Upload failures |
| `service:api-webhook` | Webhook processing errors |
| `user.id:<userId>` | All errors for a specific user |
| `enricher:weather` | Weather enricher failures |
| `destination:strava` | Strava upload failures |

#### Context Available in Sentry Events

Every Sentry event includes:
- `user.id` — FitGlue user ID
- `service` — Cloud Run service name
- `execution_id` — Unique execution identifier
- `pipeline_execution_id` — Links to PipelineRun document
- `environment` — `dev`, `test`, or `prod`

### Firestore Console

**Console**: [GCP Console → Firestore](https://console.cloud.google.com/firestore)

#### Key Collections for Debugging

| Path | Purpose | When to Check |
|------|---------|---------------|
| `users/{userId}` | User profile, integrations | Connection/auth issues |
| `users/{userId}/pipelines/` | Pipeline configurations | Pipeline CRUD issues |
| `users/{userId}/pipeline_runs/` | Execution results with booster/destination detail | Any pipeline processing issue |
| `users/{userId}/pending_inputs/` | Paused pipeline inputs | "Waiting" status issues |
| `users/{userId}/activities/` | Synced activity records | Missing activity issues |
| `integrations/{provider}/ids/{externalId}` | Reverse-lookup: external ID → FitGlue user | Webhook user resolution failures |

---

## Related Documentation

- [Architecture Overview](../architecture/overview.md) — System topology and data flow
- [Go Services](../architecture/go-services.md) — Service directory map
- [Error Codes Reference](../reference/errors.md) — All error codes and retryability
- [Plugin System](../architecture/plugin-system.md) — Source, enricher, destination architecture
- [Testing Guide](../development/testing.md) — How to write and run tests
- [Enricher Testing](./enricher-testing.md) — Testing enrichment providers
