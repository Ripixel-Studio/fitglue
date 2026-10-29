# Connecting sources from the box (`fitgluectl connect`)

The React web app used to be the only way to connect Strava, Fitbit, Hevy,
Intervals.icu and GitHub. It is retired; James (re)connects every provider from
a terminal on the box with `fitgluectl`. The OAuth **callback** is still an
HTTP route on `api-client` (`GET /api/v2/oauth/{provider}/callback`), so the
box needs that service reachable from the public internet at the URL the
provider was told about — today `https://fitglue.tech`, after F3.3 the
Tailscale Funnel URL.

The authorise URL, the signed `state` and the callback URL are built in one
place, `src/go/internal/oauthflow/`, used by both the CLI and `api-client`.
If you change scopes or endpoints, change them there.

## Environment the CLI needs

`fitgluectl` reads the same variables `api-client` runs with. On the box they
are in `~/.jarvis/fitglue.env` (F3.5, [`box-secrets.md`](box-secrets.md));
export them for the shell you run the command in with
`set -a; . ~/.jarvis/fitglue.env; set +a` (never commit or paste them into
Slack):

| Variable | Used by | Notes |
|---|---|---|
| `FITGLUE_OWNER_UID` | `connect`, `connections` | the owner's `users/{uid}` document id (F2.1). `--uid` overrides. |
| `OAUTH_STATE_SECRET` | `connect <oauth provider>` | **must be the value api-client runs with** — the callback verifies the state with it; a mismatch redirects with `error=invalid_state_signature`. |
| `FITGLUE_PUBLIC_BASE_URL` | `connect` | public origin of api-client, no trailing slash. Falls back to `API_URL` (the Cloud Run name). The callback is `<base>/api/v2/oauth/<provider>/callback`; it must match what the provider's developer app has registered. On the box the base is `https://jarvis.tail3065f7.ts.net/fg`, so register `https://jarvis.tail3065f7.ts.net/fg/api/v2/oauth/<provider>/callback`. |
| `GOOGLEHEALTH_CLIENT_ID`, `STRAVA_CLIENT_ID`, `FITBIT_CLIENT_ID`, `GITHUB_CLIENT_ID`, `SPOTIFY_CLIENT_ID` | `connect <that provider>` | only the id is needed to print a URL; the secret is used by the callback's token exchange, on the server side. |
| `PROJECT_ID` | `connect hevy/intervals`, `connections` | Firestore project; defaults to `fitglue-server-prod`. `--project` overrides. |

Firestore access uses Application Default Credentials (`gcloud auth
application-default login`, or the service-account key the box already has).
`connect <oauth provider>` never opens Firestore — printing a URL is
side-effect free — so it needs no credentials at all.

## What is connected right now

```bash
fitgluectl connections            # table
fitgluectl connections --json     # same rows as JSON
```

Columns: provider, enabled, connected (the integration's `created_at`), token
expiry (OAuth providers; refresh happens automatically on use), whether a
credential is present, and the provider-side id (Strava athlete id, Fitbit
user id, ...). Tokens and keys are never printed. Rows come straight from the
`integrations` map on the owner's user document — the same read the `user`
service's `ListIntegrations` does.

## OAuth providers: Google Health, Strava, GitHub, Spotify (and legacy Fitbit)

```bash
fitgluectl connect strava
```

prints one line: the provider's authorise URL, with the owner's uid in an
HMAC-signed `state` that expires in 10 minutes. Open it in any browser, approve,
and the provider redirects to the callback on `api-client`, which exchanges the
code, stores the tokens under `users/{uid}.integrations.{provider}` and then
redirects the browser to `$WEB_URL/app/connections/{provider}/success`. With
the web app gone that final redirect lands on a 404 page; **that is fine** —
the integration was written before the redirect. Confirm with
`fitgluectl connections`.

Reconnecting (expired refresh token, scope change, re-created developer app)
is the same command; the callback overwrites the integration document.

### Strava

- Scopes: `read_all,activity:read_all,activity:write` (comma-separated: Strava
  is the one provider that rejects space-separated scopes).
- Developer app: <https://www.strava.com/settings/api>. The *Authorization
  Callback Domain* is a bare host (`fitglue.tech`; after F3.3
  `jarvis.tail3065f7.ts.net`). Strava compares only the host, so a path prefix
  on the Funnel URL is fine.
- The callback stores `athlete_id` from the token response; `api-webhook`
  resolves incoming events by it (`integrations.strava.athlete_id`).
- **Webhook subscription (F3.4 b)** is per app, not per user, and is separate
  from connecting. One subscription exists for the app; its `callback_url` is
  `<public base>/api/webhooks/strava` and the verify token is
  `STRAVA_WEBHOOK_VERIFY_TOKEN`. On the box the callback is
  `https://jarvis.tail3065f7.ts.net/fg/webhook/strava` and
  `deploy/box/cutover.sh --strava` does the re-point (`box-cutover.md` §5).
  By hand, to inspect / re-point it when the base URL changes:
  ```bash
  # view
  curl -G https://www.strava.com/api/v3/push_subscriptions \
    -d client_id=$STRAVA_CLIENT_ID -d client_secret=$STRAVA_CLIENT_SECRET
  # Strava has no update call: delete, then create with the new callback_url
  curl -X DELETE "https://www.strava.com/api/v3/push_subscriptions/<id>?client_id=$STRAVA_CLIENT_ID&client_secret=$STRAVA_CLIENT_SECRET"
  curl -X POST https://www.strava.com/api/v3/push_subscriptions \
    -F client_id=$STRAVA_CLIENT_ID -F client_secret=$STRAVA_CLIENT_SECRET \
    -F callback_url=<public base>/api/webhooks/strava \
    -F verify_token=$STRAVA_WEBHOOK_VERIFY_TOKEN
  ```
  The create call makes Strava `GET` the callback with `hub.challenge` and the
  verify token; `api-webhook` must already be serving at the new URL or the
  create fails.

### Google Health (heart rate, sleep, body, food)

The Google Health API (`health.googleapis.com/v4`) replaced the Fitbit Web API,
which Google turns off on **2 Nov 2026** (support ended 30 Sep;
<https://developers.google.com/health/migration>). Fitbit tokens do not carry
over: this is a fresh Google OAuth consent. The heart-rate enricher
(`fitbit-heart-rate`, the name pipelines store) reads it; nothing calls the
Fitbit API for heart rate any more.

- Endpoints: authorise `https://accounts.google.com/o/oauth2/v2/auth` with
  `access_type=offline` (refresh token) and `prompt=consent` (a refresh token
  on a re-connect too, and the scope checkboxes again); token
  `https://oauth2.googleapis.com/token`, client id/secret in the form body.
- Scopes (read-only, all four asked for at consent; the sleep/body/food
  routes below each need their own):
  `https://www.googleapis.com/auth/googlehealth.health_metrics_and_measurements.readonly`
  (heart rate, resting HR, HRV, weight — **the one heart rate needs**),
  `…/googlehealth.activity_and_fitness.readonly` (exercise sessions, steps),
  `…/googlehealth.sleep.readonly`, `…/googlehealth.nutrition.readonly`.
  Untick heart rate at consent and every enrichment fails with
  `MISSING_OAUTH_SCOPE`; reconnect.
- Google Cloud: project `fitglue-server-prod` (FitGlue's own; it already has
  the Google consent screen from the old Sheets destination). Enable *Google
  Health API* (`health.googleapis.com`, also in `terraform/apis.tf`), add the
  four scopes on the consent screen's *Data Access* page, keep *Audience* at
  **In production** (Testing expires refresh tokens after 7 days), and create
  a **Web application** OAuth client whose *Authorized redirect URI* is
  exactly `https://jarvis.tail3065f7.ts.net/fg/api/v2/oauth/googlehealth/callback`
  — what `oauthflow` builds from `FITGLUE_PUBLIC_BASE_URL`. The `/fg/oauth/…`
  Caddy alias reaches the same handler, but Google compares the redirect URI
  byte for byte with the one in the authorise request, so register the
  `/fg/api/v2/oauth/…` form (adding both does no harm). The app stays
  unverified: fine under 100 users, with the "Google hasn't verified this
  app" interstitial (Advanced → Go to …).
- Env: `GOOGLEHEALTH_CLIENT_ID`, `GOOGLEHEALTH_CLIENT_SECRET` in
  `~/.jarvis/fitglue.env` (api-client exchanges the code and refreshes for
  the health routes; pipeline refreshes for heart rate; the CLI prints the
  URL). Two refreshers on one grant are fine for Google — its refresh tokens
  are reusable, unlike Fitbit's single-use ones.
- Precondition: the Google account is signed in to the **Google Health app**
  on the phone (the Fitbit account migrated). Otherwise every call answers
  `400 ACCOUNT_NOT_LINKED`. The callback calls `GET /v4/users/me/identity`
  straight after the token exchange: on success it stores `health_user_id`
  (shown by `fitgluectl connections`); on failure the tokens are still stored
  but the browser lands on `…/connections?error=googlehealth_account_not_linked`
  (or `googlehealth_identity_<status>`) — fix the link and connect again.
- Reads: heart rate is `GET /v4/users/me/dataTypes/heart-rate/dataPoints`
  with `filter=heart_rate.sample_time.physical_time >= "<start>" AND
  heart_rate.sample_time.physical_time < "<end>"` (RFC 3339 UTC),
  `dataSourceFamily=users/me/dataSourceFamilies/google-wearables` (Google and
  Fitbit trackers, no manual entries) and `pageSize=10000`, following
  `nextPageToken`. Physical time means no profile timezone lookup and no
  midnight split, which the Fitbit intraday API needed. Limit: 300
  requests/min/user.

```bash
fitgluectl connect googlehealth     # open the URL, tick every box
fitgluectl connections              # googlehealth row, external id = health user id
```

#### Sleep, body and food routes (L0.1b)

The owner's API serves them from the same grant (`internal/health`, client in
`pkg/integrations/googlehealth/daily.go`); Jarvis reads them and never calls
Google itself. Bearer token as every `/users/me` route; `?days` is 1–90; the
window is today (the box's civil day) and the `days − 1` days before it.

| Route | Default | Shape | Needs scope |
|---|---|---|---|
| `GET /api/v2/users/me/sleep?days=7` | 7 | `{nights[{date, start, end, minutes_asleep, efficiency, stages?{deep, light, rem, wake}, nap?, main_sleep?}]}` — oldest first, naps included | `googlehealth.sleep.readonly` |
| `GET /api/v2/users/me/body?days=30` | 30 | `{weights[{date, kg}], body_fat[{date, pct}], resting_hr[{date, bpm}], hrv[{date, ms}]}` — one per day, oldest first | `googlehealth.health_metrics_and_measurements.readonly` |
| `GET /api/v2/users/me/nutrition?days=7` | 7 | `{days[{date, kcal, protein_g, carbs_g, fat_g, meals[{type, name, kcal, at}]}]}` — days with anything logged, oldest first | `googlehealth.nutrition.readonly` |

- **404 `NOT_CONNECTED`** when there is no enabled grant, the stored scope
  list lacks the route's scope, or Google answers 403 / `ACCOUNT_NOT_LINKED`;
  Jarvis reads 404 as "not connected". 400 for a bad `days`, 502 when Google
  fails otherwise. A 200 is cached in memory for 5 minutes per route and
  window.
- Sleep is the J9.4 shape Jarvis's readiness card reads (`docs/home-screen.md`
  in the jarvis repo, "The sleep route"): `date` is the wake date (civil end
  date), `start`/`end` carry Google's offset, `efficiency` is
  `minutesAsleep / minutesInSleepPeriod × 100` (Google has no efficiency
  field), `stages` only for stage-tracked logs. There is no sleep score in
  the API, so `score` is never sent. Read with `sleep/dataPoints:reconcile`,
  filter `sleep.interval.civil_end_time >= "<from>"`, 25 a page; sleep
  refuses an explicit `dataSourceFamily`.
- Weight and body fat are `dataPoints:dailyRollUp` averages per day
  (`weightGramsAvg`, `bodyFatPercentageAvg`; ≤ 90 days a call), so the same
  weigh-in arriving twice (Renpho through Health Connect *and* as
  `GOOGLE_WEB_API`) does not count twice. Resting HR and HRV are not roll-up
  types: `daily-resting-heart-rate` / `daily-heart-rate-variability`
  `dataPoints` with `daily_resting_heart_rate.date >= "<from>"` (filters take
  snake_case names).
- Food: day totals from `nutrition-log` `dailyRollUp` (energy `kcalSum`,
  `PROTEIN` nutrient, `totalCarbohydrate`, `totalFat`; summed from the entries
  if a day has no roll-up), meals from `nutrition-log/dataPoints` with
  `nutrition_log.interval.civil_start_time >= "<from>"`. `type` is the
  lower-cased `mealType`, or `null` — MacroFactor writes none.
- Everything except sleep asks for `dataSourceFamily=all-sources` (the
  default, stated anyway): the food (MacroFactor, `com.sbs.diet`) and the
  scale (Renpho, `com.renpho.health`) reach Google Health through Health
  Connect, which `google-wearables` would drop.

Check what exists without printing a value — counts, date ranges and the
writing apps only, through the same code the routes run (read-only):

```bash
set -a; . ~/.jarvis/fitglue.env; set +a
GOOGLE_APPLICATION_CREDENTIALS=~/.jarvis/fitglue-box-sa.json bin/fitgluectl health-check [--sleep-days 7 --body-days 30 --nutrition-days 7]
```

### Fitbit (legacy — turned off 2 Nov 2026)

The Fitbit Web API and Fitbit OAuth endpoints stop working on 2 Nov 2026.
Heart rate has moved to Google Health (above). What remains on Fitbit is the
activity **source** (webhook + history sync), kept until James confirms
whether he still records activities on Fitbit (life-companion spec Q1). When
it goes, it goes as one change — delete:

- the webhook source: `src/go/services/api-webhook/internal/webhook/sources/fitbit/`,
  its registration and `FITBIT_SUBSCRIBER_VERIFICATION_TOKEN` read in
  `services/api-webhook/main.go`, `registerFitbitRoutes` in
  `services/api-webhook/internal/server/server.go`, `FitbitVerification` /
  `FitbitEvent` in `src/proto/gateway/webhook.proto` (`make generate`)
- the history-sync source: `src/go/pkg/sourceplugins/fitbit.go` and its
  generated client `src/go/pkg/api/fitbit/`
- the old heart-rate client `src/go/pkg/integrations/fitbit/` (+ `src/openapi/fitbit/`)
  — **already unused**; nothing imports it since the enricher moved
- the `fitbit` cases in `internal/oauthflow` (+ its test),
  `pkg/infrastructure/oauth/token_source.go` (+ Basic auth),
  `services/api-client/internal/server/oauth.go` (Basic auth, `fitbit_user_id`),
  `internal/user/firestore_store.go` (`FindUserByIntegration`),
  `pkg.TopicFitbitUpdates` (no reader)
- env `FITBIT_CLIENT_ID`, `FITBIT_CLIENT_SECRET`, `FITBIT_OAUTH_CLIENT_SECRET`,
  `FITBIT_SUBSCRIBER_VERIFICATION_TOKEN` (`deploy/box/fitglue.env.example`,
  `~/.jarvis/fitglue.env`, `terraform/secrets.tf`, `terraform/cloud_run.tf`),
  `scripts/configure_fitbit_verification.sh`, `docs/guides/fitbit-setup.md`,
  this section
- keep `FitbitIntegration` in the proto (reserve the field when removing it)
  and `SOURCE_FITBIT` (stored on old activities)

The rest of this section describes it as it was.

- Scopes: `activity profile heartrate`.
- Developer app: <https://dev.fitbit.com/apps>, OAuth 2.0 application type
  *Server*. Fitbit requires the **full callback URL** registered exactly:
  `<public base>/api/v2/oauth/fitbit/callback`. (`api-client` also serves the
  pre-v2 alias `/auth/fitbit/callback`, which is what the current developer
  app has registered; either works as long as the registered value and
  `FITGLUE_PUBLIC_BASE_URL` agree on the host.)
- The token exchange uses HTTP Basic auth with the client id/secret (Fitbit
  and Spotify do; Strava and GitHub take them in the form body). That is in
  the callback handler, not in anything the CLI prints.
- The callback stores `fitbit_user_id`; `api-webhook` resolves notifications
  by it.
- **Subscriber verification (F3.4 c)** is set in the Fitbit developer console,
  not by API: under the app's *Subscriber* settings add the endpoint
  `<public base>/api/webhooks/fitbit` with the verification code
  `FITBIT_SUBSCRIBER_VERIFICATION_TOKEN`; Fitbit then `GET`s it with
  `?verify=<code>` and expects 204 (`api-webhook` returns 204 on a match, 404
  otherwise). Change the endpoint there when the public base URL changes:
  on the box it is `https://jarvis.tail3065f7.ts.net/fg/webhook/fitbit`
  (`box-cutover.md` §6).
- **Per-user subscription is a separate API call and nothing in the Go code
  makes it** (the old guide's claim that the web app did it automatically was
  wrong; `services/api-webhook/.../fitbit` only verifies and receives). After
  connecting, subscribe the owner's activities once, with the stored access
  token (take it from Firestore; it expires in 8h, so do this soon after
  connecting):
  ```bash
  curl -X POST -H "Authorization: Bearer <access_token>" \
    https://api.fitbit.com/1/user/-/activities/apiSubscriptions/owner.json
  ```
  `owner` is the subscription id we choose; one per Fitbit user. A `200`/`201`
  with `apiSubscriptions` in the body means Fitbit will now POST to the
  subscriber endpoint. The subscription survives token refreshes but not a
  revoke-and-reconnect, so repeat it after a reconnect from scratch.

### GitHub

- Scope: `read:user`. The callback stores the token; `github_user_id` is **not**
  set by the callback (the token response has no user id), so
  `integrations.github.github_user_id` stays empty unless written by hand. The
  GitHub webhook source has no HTTP route on `api-webhook` (only strava, fitbit
  and hevy are routed), so this integration today only feeds the GitHub
  *destination* uploader.
- Developer app: GitHub OAuth App (not a GitHub App), *Authorization callback
  URL* `<public base>/api/v2/oauth/github/callback`.

### Spotify

Spotify was never a web-app connection either (it is an enricher's data
source), but it has an authorise configuration, so `fitgluectl connect
spotify` works the same way. Scopes `user-read-recently-played
user-read-playback-state`; Basic-auth token exchange like Fitbit.

## API-key providers

### Hevy

```bash
printf '%s\n' "<hevy personal api key>" | fitgluectl connect hevy --api-key-stdin
```

(`--api-key <key>` also works but lands in shell history.) This does what the
web app's `PUT /users/me/integrations/hevy` did:

1. writes `users/{uid}.integrations.hevy = {apiKey, enabled: true,
   consent_given: true, connected_at, created_at}` (`apiKey` is camelCase
   because that is what the web app sent and what protojson reads back into
   `HevyIntegration.api_key`; `created_at` is new so `connections` can show
   it);
2. mints a random **ingress key**, stores only its hash in
   `ingress_api_keys/{hash}` with `user_id`, label `hevy-webhook` and scope
   `webhook:hevy`, and prints the raw key **once**.

`api-webhook` resolves a Hevy event to the owner by that ingress key
(`FindUserByIntegration("hevy", key)` looks the hash up), then fetches the
workout with the stored personal API key. So after running the command,
register the webhook in Hevy (Settings → Developer → Webhooks, or
`POST https://api.hevyapp.com/v1/webhook-subscription` with the personal key
in the `api-key` header) with:

- URL: `<public base>/api/webhooks/hevy` — on the box
  `https://jarvis.tail3065f7.ts.net/fg/webhook/hevy` (`box-cutover.md` §7 has
  the GET/POST/DELETE calls)
- auth token: the printed ingress key (Hevy sends it as `Authorization`;
  `api-webhook` also accepts `X-Api-Key` and `?api_key=`).

Re-running `connect hevy` mints a new ingress key; old hashes are not deleted,
so a previously registered webhook keeps working until you remove its doc.

### Intervals.icu

```bash
printf '%s\n' "<intervals api key>" | fitgluectl connect intervals --api-key-stdin --athlete-id i12345
```

Verifies the pair against `https://intervals.icu/api/v1/athlete/{id}` first
(like the web flow) and stores `{api_key, athlete_id, enabled, consent_given,
connected_at, created_at}`. Intervals.icu is a destination (upload) and a
history source; its webhook source has no route on `api-webhook`.

## Not covered

- **TrainingPeaks** — there has never been an authorise configuration for it:
  `oauthflow.Provider("trainingpeaks")` is nil and the web app had no connect
  button. The token *refresh* path exists (`pkg/infrastructure/oauth`,
  `https://oauth.trainingpeaks.com/token`), so an integration document written
  by hand with a valid `refresh_token` and `athlete_id` would keep working, but
  `fitgluectl connect trainingpeaks` refuses rather than guess at the
  authorise endpoint and scopes. Add a `ProviderConfig` to `oauthflow` once
  the TrainingPeaks partner app details are known.
- **Parkrun** needs only `integrations.parkrun.athlete_id`, written via the
  `user` service (`SetIntegration`) — no credential, no CLI command yet.

## Troubleshooting

| Symptom | Cause |
|---|---|
| callback redirects with `error=invalid_state_signature` | `OAUTH_STATE_SECRET` in your shell differs from api-client's |
| `error=state_token_expired` | more than 10 minutes between `connect` and approving |
| provider says redirect URI mismatch | `FITGLUE_PUBLIC_BASE_URL` (or `API_URL`) does not match the developer app's registered callback |
| `error=token_exchange_error` | wrong client secret on the api-client side, or the code was used twice (every URL is single use — run `connect` again) |
| `connections` shows the provider but `api-webhook` ignores events | Strava: `athlete_id` missing; Fitbit: no `apiSubscriptions` call made; Hevy: webhook registered with a stale ingress key |
