# Debugging Guide

This guide covers debugging techniques for FitGlue services. For a complete per-domain troubleshooting playbook, see the [Troubleshooting Guide](../guides/troubleshooting.md).

## General Debugging

### Cloud Logging

All FitGlue services emit structured JSON logs via Go's `slog` package. Every log entry includes:
- `service` — Cloud Run service name
- `user_id` — FitGlue user ID (when available)
- `pipeline_execution_id` — Links to PipelineRun (when applicable)
- `execution_id` — Unique execution identifier

**Quick filter to find errors for a specific user:**
```
resource.type="cloud_run_revision"
jsonPayload.user_id="<userId>"
severity>=WARNING
```

**Quick filter to trace a pipeline execution end-to-end:**
```
resource.type="cloud_run_revision"
jsonPayload.pipeline_execution_id="<id>"
```

### Sentry

All services integrate Sentry via the `SentryHandler` pattern. Error-level `slog` messages are automatically captured as Sentry exceptions with full context.

**Console**: [fitglue.sentry.io/issues/](https://fitglue.sentry.io/issues/)

### Firestore Inspection

Use the [GCP Firestore Console](https://console.cloud.google.com/firestore) to inspect:
- `users/{userId}/pipeline_runs/` — Execution results with per-booster and per-destination outcomes
- `users/{userId}/pending_inputs/` — Paused pipeline inputs awaiting resolution
- `users/{userId}/pipelines/` — Pipeline configurations

### Running a Single Service Locally

For focused debugging, run an individual service as a Go binary:

```bash
cd src/go
go run ./services/pipeline/...    # Pipeline service
go run ./services/destination/...  # Destination service
go run ./services/api-webhook/...  # Webhook processor
```

Each service reads configuration from environment variables (see `.env`).

---

## Fitbit Integration Debugging

### Fitbit Debug Script

The `scripts/debug-fitbit.ts` script allows you to manually inspect the Fitbit API response for a specific user and date. It checks:
1.  **Activity List**: Fetches the list of activities for the given date.
2.  **Processing Status**: Checks if the activity has already been processed in Firestore.
3.  **TCX Availability**: Attempts to fetch the TCX file for each activity.
4.  **Raw API Response**: If the client fails (e.g., 403 Forbidden), it attempts a raw fetch to inspect headers and the raw error body.

### Usage

Run the script using `ts-node` from the `server` directory:

```bash
npx ts-node scripts/debug-fitbit.ts <USER_ID> <DATE>
```

-   `<USER_ID>`: The FitGlue User UUID (not the Fitbit ID). You can find this in the Firestore `users` collection or execution logs.
-   `<DATE>`: The date in `YYYY-MM-DD` format.

### Example

```bash
npx ts-node scripts/debug-fitbit.ts 832bc50d-4814-4fce-89ff-f94ef4bba9b1 2026-01-01
```

### Common Issues

-   **403 Forbidden on TCX Fetch**: This usually indicates that the `location` scope was not granted during authentication. The user must reconnect Fitbit with the correct scopes (`activity heartrate profile location`).
-   **No TCX Data**: Not all Fitbit activities have TCX data. Manual logs or auto-detected walks often do not.

---

## Related Documentation

- [Troubleshooting Guide](../guides/troubleshooting.md) — Per-domain debugging playbooks
- [Error Codes Reference](../reference/errors.md) — All error codes and retryability
- [Local Development](local-development.md) — Running the full stack locally
