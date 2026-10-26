# Cut-over: pointing the inputs at the box (F3.4)

The services are built and installed on the box (F3.1), the box has an
identity (F3.2), a public door (F3.3) and — after F3.5 — its secrets. This
guide moves the *inputs* there: Pub/Sub pushes, the scheduler jobs, the three
provider webhooks and the parkrun agent. Each step is reversible on its own
and the cloud deployment keeps working until the last one.
`deploy/box/cutover.sh` is this page as a script; the flags below are its
flags.

```
                 before                                after
Pub/Sub push ──▶ Cloud Run pipeline/activity/destination   ──▶ https://jarvis.tail3065f7.ts.net/fg/pubsub/… → 810x
Cloud Scheduler ─▶ topic-roundup-trigger / parkrun-trigger   systemd user timers on the box → fitgluectl trigger
Strava ────────▶ https://fitglue.tech/api/webhooks/strava   ──▶ …/fg/webhook/strava
Fitbit ────────▶ https://fitglue.tech/api/webhooks/fitbit   ──▶ …/fg/webhook/fitbit
Hevy ──────────▶ https://fitglue.tech/api/webhooks/hevy     ──▶ …/fg/webhook/hevy
parkrun agent ─▶ https://pipeline-….run.app/internal/…      ──▶ http://127.0.0.1:8104/internal/…
```

Public base: `https://jarvis.tail3065f7.ts.net/fg` (Funnel → Caddy,
`docs/guides/box-ingress.md`).

## Order

| # | Step | How | Reversible by |
|---|---|---|---|
| 0 | Preconditions | `cutover.sh --check` | — (read-only) |
| 1 | Merge the F3.4 PR | CI applies terraform: push subscriptions → box, Scheduler jobs gone, push self-invoker grants gone | revert the PR |
| 2 | Start the box | `cutover.sh --start` | `systemctl --user stop fitglue.target` |
| 3 | Verify pushes | `cutover.sh --pubsub` | — |
| 4 | Parkrun agent | `cutover.sh --parkrun` | delete `~/.jarvis/parkrun-agent.env` |
| 5 | Strava webhook | `cutover.sh --strava` | same command with the old callback (by hand, below) |
| 6 | Fitbit subscriber | James, dev console (below) | change it back |
| 7 | Hevy webhook | API call with the personal key (below) | same call with the old URL |
| 8 | Proof | `cutover.sh --scale-down-cloud`, then a real activity | `cutover.sh --rollback` |

Between 1 and 2 there is a window in which Pub/Sub pushes to the box and
nothing listens: Pub/Sub retries with backoff (10 s → 10 min, `pubsub.tf`) and
the messages are retained (1 h on the activity topics, 24 h on the lag topic),
so keep the window short and nothing is lost. Do not start the box *before*
the terraform applies: then both deployments would be live, and while that is
harmless for pushes (Pub/Sub only delivers where the subscription says) it is
confusing for logs.

## 0. `--check`

Read-only. Funnel mapping, Caddy up and answering 404, every unit linked and
loadable (and no stale `fitglue-api-public.service`), binaries in `bin/`,
`~/.jarvis/fitglue.env` complete with `PUBSUB_PUSH_AUDIENCE` exactly
`https://jarvis.tail3065f7.ts.net/fg/pubsub`, the SA key readable and
`fitglue-box@…`, and a Firestore read with that key (`fitgluectl connections`).
It also reports how many push subscriptions already point at the box, so it
doubles as a "did terraform apply?" check.

**Known state on 2026-10-26.** The `fitglue-box` service account that F3.2
created by hand was destroyed by CI at 23:33:55 on 2026-10-25: CircleCI's
`deploy-prod` runs are not serialised, the run for F3.2's commit imported the
account into state, and the run for F3.3's commit (merged ten seconds earlier,
config without it) took the lock next and removed it. Every `deploy-prod` on
`main` since 23:30 that night has failed, so **nothing merged since F2.3 has
been applied** (api-public and the Scheduler jobs still exist). Before step 1
can mean anything:

1. Find out why `deploy-prod` fails (CircleCI job output; the box has no
   CircleCI token in a shell it may read) and fix it. The F3.4 PR removes the
   now-dangling `import` block for the account, so terraform creates it afresh.
2. After the first successful apply, James mints a new key for the new account
   (a key is bound to the account's unique id, so the old file is dead):
   `gcloud iam service-accounts keys create ~/.jarvis/fitglue-box-sa.json
   --iam-account fitglue-box@fitglue-server-prod.iam.gserviceaccount.com &&
   chmod 600 ~/.jarvis/fitglue-box-sa.json`.
3. `cutover.sh --check` must then pass the Firestore read.

`--check` reports the dead key as
`'account not found' means fitglue-box@… was deleted or recreated`.

## 1. Pub/Sub and the schedulers (terraform, applied by CI on merge)

`terraform/pubsub.tf`: every push subscription's endpoint is now
`https://jarvis.tail3065f7.ts.net/fg/pubsub/<svc>/<path>` and every
`oidc_token` block carries `audience = "https://jarvis.tail3065f7.ts.net/fg/pubsub"`
(`local.box_pubsub_audience`). The signing account is still the per-service
`cr-<svc>-sa`; the services check the token in-process
(`infra.PubSubPushAuthFromEnv`) against `PUBSUB_PUSH_AUDIENCE`, which **must be
that exact string** in `~/.jarvis/fitglue.env`. `--check` fails if it is not.

`terraform/scheduler.tf` is deleted. Its four jobs are systemd **user** timers
in `deploy/box/`, same schedules, same message bodies:

| Timer | OnCalendar | Publishes |
|---|---|---|
| `fitglue-roundup-weekly.timer` | `Mon *-*-* 06:00:00 UTC` | `topic-roundup-trigger` `{"period_type":"week"}` |
| `fitglue-roundup-monthly.timer` | `*-*-01 06:00:00 UTC` | `topic-roundup-trigger` `{"period_type":"month"}` |
| `fitglue-roundup-yearly.timer` | `*-01-01 06:00:00 UTC` | `topic-roundup-trigger` `{"period_type":"year"}` |
| `fitglue-parkrun-check.timer` | `*-*-* 10,12,14,16,18,20:00:00 Europe/London` | `topic-parkrun-results-trigger` `{}` |

Each `.service` runs `bin/fitgluectl trigger <roundup|parkrun-check> [--data …]`
with the fitglue-box key (pubsub.publisher). `Persistent=true` replays one
missed fire after a reboot, as Scheduler's single retry did. They are
`Wants=` by `fitglue.target` and `PartOf=` it, so `--start` starts them and
`stop fitglue.target` stops them. Fire one by hand:
`systemctl --user start fitglue-roundup-weekly.service`; dry-run the CLI:
`bin/fitgluectl trigger roundup --data '{"period_type":"week"}' --dry-run`.

`cloud_run.tf` loses the `pubsub_self_invoke` grants (the `cr-*-sa` accounts'
`run.invoker` on their own services, which only existed so Cloud Run IAM would
admit the pushes). The `cloudscheduler.googleapis.com` API stays enabled until
F4.2's terraform diet.

## 2. `--start`

`systemctl --user enable --now fitglue.target`, then waits up to 60 s per
service for `/health` (api-webhook `/api/webhooks/health`, api-client,
pipeline, activity) or the port (user, destination), lists the timers, and
fetches `https://jarvis.tail3065f7.ts.net/fg/webhook/health` through Funnel.

## 3. `--pubsub`

Lists the seven push subscriptions with endpoint, signing account and
audience, failing any that does not match `pubsub.tf`, then POSTs an
unauthenticated envelope to each public push path and expects **401** (no
token) or 403. 404 means Caddy has no route, 502 means the service is down,
and anything 2xx/4xx-other means the service accepted an unauthenticated push:
`PUBSUB_PUSH_AUDIENCE` is unset in its environment — stop the target.

A real push then shows in `journalctl --user -u fitglue-pipeline` as the
handler's normal log lines; a 403 there with "pub/sub push auth" means the
audience differs between terraform and the env file.

## 4. `--parkrun`

Writes `~/.jarvis/parkrun-agent.env`:

```
FITGLUE_PIPELINE_URL=http://127.0.0.1:8104
PARKRUN_AGENT_SA_KEY=/home/jarvis/.jarvis/fitglue-box-sa.json
```

`jarvis-parkrun-agent.service` (jarvis repo) loads it after `~/.jarvis/env`
with `EnvironmentFile=-`, so until it exists the agent keeps using Cloud Run
and the `parkrun-agent` key. The agent mints its identity token for
`https://127.0.0.1:8104` — the pipeline's `/internal/*` handlers verify
`"https://" + r.Host` (`parkrun_agent.go`), and on loopback `r.Host` is
`127.0.0.1:8104` even though the hop is plain HTTP. `FITGLUE_PIPELINE_AUDIENCE`
overrides that derivation if ever needed. The script then runs the agent with
`--dry-run` against the box pipeline (lists pending inputs, posts nothing) to
prove the token is accepted. If the installed system unit predates the
`EnvironmentFile=-` line: `jarvis-privileged install-units`.

The `parkrun-agent` service account and its `run.invoker` on the Cloud Run
pipeline stay in terraform until F4 (the agent could still be pointed back at
Cloud Run by deleting the env file).

## 5. Strava — `--strava`

Strava's webhook subscription is per app, one per app, and has no update
call: re-pointing is delete + create. The script reads the client id, client
secret and verify token from `~/.jarvis/fitglue.env` (falling back to Secret
Manager `strava-client-id`, `strava-client-secret`, `strava-verify-token`),
never prints them, and:

1. `GET https://www.strava.com/api/v3/push_subscriptions?client_id&client_secret`
   — shows the current `id` and `callback_url`. If it already is
   `https://jarvis.tail3065f7.ts.net/fg/webhook/strava`, done.
2. Pre-flights the handshake the create will trigger:
   `GET <new callback>?hub.mode=subscribe&hub.verify_token=<token>&hub.challenge=<x>`
   must return `{"hub.challenge":"<x>"}` from the box (api-webhook up, same
   verify token on both sides). Without this the create fails and the old
   subscription is already gone.
3. Asks `YES`, then
   `DELETE https://www.strava.com/api/v3/push_subscriptions/<id>?client_id&client_secret` (204)
   and `POST https://www.strava.com/api/v3/push_subscriptions` with form fields
   `client_id`, `client_secret`, `callback_url=https://jarvis.tail3065f7.ts.net/fg/webhook/strava`,
   `verify_token` (returns `{"id":…}`).

Events in the gap between delete and create are lost; it is a second or two.
Rollback is the same three calls with `callback_url=https://fitglue.tech/api/webhooks/strava`
while Cloud Run api-webhook still serves there. The Strava app's
*Authorization Callback Domain* (`https://www.strava.com/settings/api`) is for
OAuth, not webhooks; F2.3's `connect-sources.md` covers it.

## 6. Fitbit — James, in the developer console

Fitbit's subscriber endpoint is configured in the app, not by API.
At <https://dev.fitbit.com/apps> → the FitGlue app → *Subscriber* settings,
set **exactly**:

| Field | Value |
|---|---|
| Endpoint URL | `https://jarvis.tail3065f7.ts.net/fg/webhook/fitbit` |
| Verification code | the value of `FITBIT_SUBSCRIBER_VERIFICATION_TOKEN` in `~/.jarvis/fitglue.env` (Secret Manager `fitbit-verification-code`) — must be the same one api-webhook runs with |
| Subscriber ID | keep what is there (the per-user `apiSubscriptions` call in `connect-sources.md` refers to it) |

On save Fitbit GETs the endpoint twice: `?verify=<the code>` expecting **204**
and `?verify=<a wrong code>` expecting **404**. api-webhook does exactly that
(`sources/fitbit`), so the services must be running (`--start`) first. Then
Fitbit POSTs notifications there, signed with `X-Fitbit-Signature` (HMAC of
the body with the client secret, `FITBIT_OAUTH_CLIENT_SECRET`).

The per-user subscription (`POST …/activities/apiSubscriptions/owner.json`)
is independent of the endpoint URL and does not need redoing.

## 7. Hevy — API call with the personal key

Hevy delivers workout events to one webhook per account, managed through its
public API with the account's personal API key (the one `fitgluectl connect
hevy` stored, `users/{uid}.integrations.hevy.apiKey`). The
`/v1/webhook-subscription` route is live (`GET` → 401 without a key) but
absent from Hevy's published Swagger, so the field names below come from
their developer page — confirm with the `GET` first:

```bash
HEVY_KEY=<personal api key>      # from the integration document, never in a repo
# what is registered now
curl -s -H "api-key: $HEVY_KEY" https://api.hevyapp.com/v1/webhook-subscription
# re-point (replaces the existing subscription)
curl -s -X POST -H "api-key: $HEVY_KEY" -H 'Content-Type: application/json' \
  https://api.hevyapp.com/v1/webhook-subscription \
  -d '{"url":"https://jarvis.tail3065f7.ts.net/fg/webhook/hevy","authToken":"<ingress key>"}'
# remove
curl -s -X DELETE -H "api-key: $HEVY_KEY" https://api.hevyapp.com/v1/webhook-subscription
```

`<ingress key>` is the key `fitgluectl connect hevy` printed once (stored
hashed in `ingress_api_keys/`); Hevy sends it back as the `Authorization`
header and api-webhook resolves the owner by it. If it is lost, re-run
`fitgluectl connect hevy --api-key-stdin` to mint a new one and register that.
The same thing can be done in the Hevy app: Settings → Developer → Webhooks.

## 8. Proof — `--scale-down-cloud` and a real activity

The spec's proof is "Cloud Run scaled to zero and a real Strava activity
arrives via the box". Cloud Run rejects `max-instances=0` (`maxScale must be a
positive integer`; checked with `gcloud run services replace --dry-run`), so
the script records each service's ingress to
`~/.jarvis/fitglue-cloud-run-ingress.json` and sets all six to
`--ingress=internal`: nothing from the internet reaches the cloud deployment
any more, and the pushes already go to the box. Terraform still says
`INGRESS_TRAFFIC_ALL`, so the next CI apply undoes this — that is fine, F4
deletes the services; re-run the flag if you need the proof again.
`--rollback` restores the recorded values.

Then record an activity on Strava and watch:

```bash
journalctl --user -u fitglue-api-webhook -f          # the Strava event arrives
journalctl --user -u fitglue-pipeline -f             # raw → run → enriched
journalctl --user -u fitglue-destination -f          # uploads
gcloud logging read 'resource.type="cloud_run_revision"' --project fitglue-server-prod \
  --freshness=2h --limit 5                            # nothing new on Cloud Run
```

Once that has run clean for a week, F4.

## Rollback, whole

In reverse: `--rollback` (Cloud Run ingress), Hevy/Fitbit/Strava back to
`https://fitglue.tech/api/webhooks/<provider>`, delete
`~/.jarvis/parkrun-agent.env`, `systemctl --user stop fitglue.target`, revert
the terraform PR (subscriptions back to the Cloud Run URLs, Scheduler jobs
recreated, self-invoker grants back) and let CI apply it.
