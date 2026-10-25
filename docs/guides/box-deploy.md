# Running FitGlue on the box

Since rethink F3.1 the seven services run as systemd **user** units on the
Jarvis box, as static binaries, against the same Firestore, GCS and Pub/Sub in
`fitglue-server-prod` that Cloud Run uses. This guide is the box side; the
cloud side (Pub/Sub subscriptions, Strava/Fitbit callbacks, scheduler timers)
is re-pointed in F3.4 and torn down in F4.

```
internet ── Tailscale Funnel (443, F3.3) ── Caddy 127.0.0.1:8100 ──┐
                                                                   │  /fg/webhook/*          → 8101 api-webhook
                                                                   │  /fg/oauth/*            → 8102 api-client
                                                                   │  /fg/pubsub/pipeline/*  → 8104 /pubsub/*
                                                                   │  /fg/pubsub/activity/*  → 8105 /pubsub/*
                                                                   │  /fg/pubsub/destination → 8106 /
                                                                   ▼
          api-webhook 8101 ─┐
          api-client  8102 ─┼─ gRPC over loopback ─► user 8103, pipeline 8104, activity 8105
          api-public  8107 ─┘                        destination 8106 ─► user, activity
                                                            │
                                   Firestore · GCS · Pub/Sub (still Google Cloud)
```

Nothing listens on anything but `127.0.0.1`. The only way in from outside is
Caddy, and Caddy only forwards `/fg/*`.

## Ports

Fixed; Caddy and the units' `*_SERVICE_URL` settings depend on them.

| Port | Unit | Speaks | Calls |
|---|---|---|---|
| 8101 | `fitglue-api-webhook` | HTTP | user, pipeline, activity |
| 8102 | `fitglue-api-client` | HTTP (bearer token) | user, pipeline, activity |
| 8103 | `fitglue-user` | gRPC | — |
| 8104 | `fitglue-pipeline` | gRPC + HTTP (`/pubsub/raw`, `/pubsub/run`, `/pubsub/enriched`, `/pubsub/parkrun-check`, `/internal/parkrun-*`, `/health`) | user |
| 8105 | `fitglue-activity` | gRPC + HTTP (`/pubsub/roundup`, `/health`) | — (POSTs `SITE_EXPORT_HOOK_URL` on the box after site exports) |
| 8106 | `fitglue-destination` | HTTP (`/` = Pub/Sub push) | user, activity |
| 8107 | `fitglue-api-public` | HTTP | activity — goes away with F1.5b/S4 |

gRPC between services is plaintext on loopback (`infra.GRPCDial` strips the
`http://` and dials insecure). On Cloud Run the same code path used TLS + an
OIDC token; the trust boundary here is the loopback interface.

## Files

| Path | What |
|---|---|
| `deploy/box/fitglue-<svc>.service` | one user unit per service: `ExecStart=%h/work/fitglue-server/bin/<svc>`, fixed non-secret env, journald, `Restart=on-failure` |
| `deploy/box/fitglue.target` | wants all seven; `start`/`stop` it to move them together |
| `deploy/box/install.sh` | symlinks the units into `~/.config/systemd/user/`, `daemon-reload`, enables the target (`--no-enable` to skip) |
| `deploy/box/fitglue.env.example` | every key `~/.jarvis/fitglue.env` must hold, with comments, no values |
| `deploy/box/caddy/`, `deploy/box/fitglue-caddy.service`, `deploy/box/funnel.md` | the ingress (F3.3, `docs/guides/box-ingress.md`); separate lifecycle, not part of `fitglue.target` |
| `~/.jarvis/fitglue.env` | secrets + per-box values, written by F3.5, `chmod 600` |
| `~/.jarvis/fitglue-box-sa.json` | the `fitglue-box` service-account key from F3.2 (`GOOGLE_APPLICATION_CREDENTIALS`) |
| `bin/` | output of `make build-box`; gitignored |

The units are symlinks into the checkout, so a `git pull` changes them in
place; `make deploy-box` runs `daemon-reload` for that reason. systemd calls
such units *linked*, and `systemctl --user disable` on a linked unit deletes
the symlink itself, not just the `default.target.wants` entry — re-run
`install.sh` (with `--no-enable` if that was the intent) to get it back.
`fitglue-caddy.service` (F3.3) lives alongside these but is not part of
`fitglue.target` or `make deploy-box`.

## Environment: what lives where

**In the unit file** (non-secret, fixed by the deployment, same on every
box): `PORT`, `SERVICE_NAME` (Sentry server name; Cloud Run had `K_SERVICE`),
`*_SERVICE_URL=http://127.0.0.1:<port>` for the services it calls,
`PROJECT_ID` and `GOOGLE_CLOUD_PROJECT` (`fitglue-server-prod`),
`ENVIRONMENT=prod`, `LOG_LEVEL=info`, `GOOGLE_APPLICATION_CREDENTIALS`, and
per service:

| Unit | Extra |
|---|---|
| pipeline | `ARTIFACT_BUCKET`, `GCS_ARTIFACT_BUCKET` = `fitglue-server-prod-artifacts`; `SHOWCASE_ASSETS_BUCKET` = `fitglue-server-prod-showcase-assets`; `ASSETS_BASE_URL=https://assets.fitglue.tech`; `GCP_REGION=us-central1` (Imagen endpoint) |
| activity | `ARTIFACT_BUCKET`, `SHOWCASE_ASSETS_BUCKET`, `SITE_EXPORT_SLUG=james` |
| destination | `GCS_ARTIFACT_BUCKET`, `SHOWCASE_ASSETS_BUCKET` |

**In `~/.jarvis/fitglue.env`** (`EnvironmentFile=`, loaded by every unit; a
key set there overrides the unit): `FITGLUE_API_TOKEN`, `FITGLUE_OWNER_UID`,
`PUBSUB_PUSH_AUDIENCE` (+ optional `PUBSUB_PUSH_SERVICE_ACCOUNT`),
`OAUTH_STATE_SECRET`, `FITGLUE_PUBLIC_BASE_URL` (`API_URL` is the legacy
fallback), `WEB_URL`, `SITE_EXPORT_HOOK_URL` (+ optional `SITE_EXPORT_BUCKET`,
`SITE_EXPORT_DISABLED`), `GEMINI_API_KEY`, the provider
client pairs (`STRAVA_*`, `FITBIT_*` incl. `FITBIT_OAUTH_CLIENT_SECRET` and
`FITBIT_SUBSCRIBER_VERIFICATION_TOKEN`, `TRAININGPEAKS_*`, `GITHUB_*` incl.
`GITHUB_WEBHOOK_SECRET`, `SPOTIFY_*`), `SENTRY_DSN`, and the optional
`PARKRUN_FETCHER_URL`, `SENTRY_RELEASE`. The example file has the comments.

Cloud Run also set `DESTINATION_SERVICE_URL` on pipeline; nothing reads it, so
the units do not carry it.

The file is in systemd's format, not shell: no inline comments, no `export`.

## Build, deploy, status, logs

Always from `~/work/fitglue-server` on `main` — that is the path the units'
`ExecStart` points at.

```bash
make build-box     # CGO_ENABLED=0 GOOS=linux GOARCH=amd64, -trimpath -s -w → bin/
make deploy-box    # build-box; systemctl --user daemon-reload; restart the
                   # fitglue-*.service units that are *active*
make status-box    # systemctl --user list-units fitglue.target fitglue-*.service

deploy/box/install.sh                 # first time on a box; enables fitglue.target
systemctl --user start fitglue.target # cutover (F3.4) — not before
systemctl --user stop fitglue.target  # everything down

journalctl --user -u fitglue-pipeline -f            # one service
journalctl --user -u 'fitglue-*' --since -1h        # all of them
journalctl --user -u fitglue-api-client -p warning  # warnings and worse
```

`make deploy-box` restarts only units that are running. It never starts a
stopped one, so while Cloud Run is still the live deployment you can build and
deploy freely without a box pipeline competing for Pub/Sub messages. Starting
the pipeline/destination/activity units while the Cloud Run subscriptions
still point at Cloud Run is harmless to them (Pub/Sub only pushes where the
subscription says) but pointless; starting api-webhook on the box before the
Strava/Fitbit callbacks move is likewise a no-op. The dangerous combination is
the other way round: both deployments subscribed to the same topics.

`loginctl show-user jarvis -p Linger` is `yes`, so the user manager and these
units survive logout and come back after a reboot once `fitglue.target` is
enabled.

## Pub/Sub push authentication

On Cloud Run the push endpoints were protected by IAM: each subscription
minted an OIDC token for its service account and Cloud Run rejected requests
without a valid one before they reached the code. **The services never checked
the token themselves.** On the box they are reachable from the internet
through Funnel, so the check moved into the process:

- `infra.PubSubPushAuthFromEnv` wraps every push handler (pipeline `/pubsub/*`,
  activity `/pubsub/roundup`, destination `/`).
- `PUBSUB_PUSH_AUDIENCE` unset → pass-through (Cloud Run, docker-compose).
- Set → the request must carry `Authorization: Bearer <google OIDC token>`
  that `idtoken.Validate` accepts for exactly that audience; 401 if missing,
  403 if invalid. With `PUBSUB_PUSH_SERVICE_ACCOUNT` also set, the token's
  verified `email` claim must be that account.

The audience is a fixed string, not derived from the request, because Caddy
rewrites the path (`/fg/pubsub/pipeline/raw` → `/pubsub/raw`) and Pub/Sub's
default audience is the full push URL, which would differ per subscription.
**F3.4 must set the same explicit audience on every push subscription:**

```hcl
push_config {
  push_endpoint = "https://jarvis.tail3065f7.ts.net/fg/pubsub/pipeline/raw"
  oidc_token {
    service_account_email = google_service_account.cloud_run_sa["pipeline"].email # unchanged
    audience              = "https://jarvis.tail3065f7.ts.net/fg/pubsub"          # == PUBSUB_PUSH_AUDIENCE
  }
}
```

and `~/.jarvis/fitglue.env` gets `PUBSUB_PUSH_AUDIENCE=https://jarvis.tail3065f7.ts.net/fg/pubsub`
(any string works as long as both sides agree; a URL under the Funnel host is
conventional). The signing account stays the per-service `cr-<svc>-sa` that
`pubsub.tf` already names — F3.2 deliberately kept `fitglue-box` out of push
signing, so no new IAM is needed. Those accounts go away with Cloud Run in
F4, at which point the subscriptions move to one account (and
`PUBSUB_PUSH_SERVICE_ACCOUNT` becomes a single shared value); until then a
pin would have to be per unit, so leave it unset.

The pipeline's `/internal/parkrun-*` endpoints are separate: they verify the
box parkrun agent's token against `https://<Host header>`. Once the agent
calls `http://127.0.0.1:8104` directly (F3.4e) it must mint its token for
audience `https://127.0.0.1:8104`, or that check needs the same env-driven
audience — F3.4's call.

## What is still cloud

Everything stateful:

- **Firestore** (`fitglue-server-prod`) — all data.
- **GCS** — `fitglue-server-prod-artifacts` (FIT files, payloads),
  `fitglue-server-prod-showcase-assets` (images; served as
  `https://assets.fitglue.tech`).
- **Pub/Sub** — the topics and push subscriptions; they are the glue between
  pipeline → destination and the lag/retry path. The box publishes to them
  and receives their pushes via Funnel.
- **Secret Manager** — until F3.5 copies the values into `fitglue.env`.
- **Cloud Run** — until F3.4 scales it to zero and F4 deletes it. The
  `parkrun-fetcher` service is dead weight (WAF-walled); the box agent
  replaced it.
- **Cloud Scheduler** — replaced by systemd timers in F3.4f.
- **DNS, the Firebase site, the assets load balancer** — S4/F4.

Identity for all of it is the `fitglue-box` service account key at
`~/.jarvis/fitglue-box-sa.json` (F3.2: datastore.user, pubsub
publisher/subscriber, aiplatform.user, objectAdmin on the two buckets,
run.invoker on the Cloud Run pipeline). A unit can be run against the box's
gcloud ADC instead by dropping in `UnsetEnvironment=GOOGLE_APPLICATION_CREDENTIALS`
— fine for a smoke test, not for the live deployment.

## Smoke test without cutting over

Safe at any time: `user` and `api-client` only read Firestore.

```bash
deploy/box/install.sh --no-enable
systemctl --user start fitglue-user.service fitglue-api-client.service
curl -s -H "Authorization: Bearer $(grep ^FITGLUE_API_TOKEN= ~/.jarvis/fitglue.env | cut -d= -f2-)" \
  http://127.0.0.1:8102/api/v2/users/me
systemctl --user stop fitglue-user.service fitglue-api-client.service
```

Do **not** start `pipeline`, `destination`, `activity` or `api-webhook` for a
test while Cloud Run is live unless you want two consumers.
