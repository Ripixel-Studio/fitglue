#!/usr/bin/env bash
# deploy/box/cutover.sh — the F3.4 cut-over runbook as a script (docs/guides/box-cutover.md).
#
# Moves FitGlue's inputs from Cloud Run to this box, one reversible step per flag,
# in the order the guide gives. Every step is idempotent: re-running a flag after
# it has succeeded reports the state and changes nothing.
#
#   --check             preconditions: Funnel, Caddy, units, binaries, env, SA key,
#                       Firestore read. Read-only. Run first, run often.
#   --start             systemctl --user enable --now fitglue.target; wait for each
#                       service's health/port; list the timers.
#   --pubsub            after the terraform PR has applied: confirm every push
#                       subscription targets the box and that each public push
#                       path answers 401/403 (OIDC check live), not 404/502/200.
#   --parkrun           write ~/.jarvis/parkrun-agent.env so the box parkrun agent
#                       talks to 127.0.0.1:8104 with the fitglue-box key, then a
#                       --dry-run of the agent against the box pipeline.
#   --strava            re-point the Strava webhook subscription at the box
#                       (delete + create: Strava has no update call). Asks YES.
#   --scale-down-cloud  record the Cloud Run services' ingress, then set every one
#                       to internal so nothing from the internet reaches the cloud
#                       deployment any more — the "scaled to zero" proof. Asks YES.
#   --rollback          restore the recorded Cloud Run ingress. Asks YES.
#
# Flags may be combined; they run in the order given. Secrets are read (from
# ~/.jarvis/fitglue.env and Secret Manager) but never printed.
set -euo pipefail

HOST=jarvis.tail3065f7.ts.net
BASE="https://$HOST/fg"
PUBSUB_AUDIENCE="https://$HOST/fg/pubsub"      # == terraform local.box_pubsub_audience
PROJECT=fitglue-server-prod
REGION=us-central1
CHECKOUT=${FITGLUE_CHECKOUT:-$HOME/work/fitglue-server}
BIN=${FITGLUE_BIN:-$CHECKOUT/bin}
ENVFILE=$HOME/.jarvis/fitglue.env
SA_KEY=$HOME/.jarvis/fitglue-box-sa.json
SA_EMAIL="fitglue-box@$PROJECT.iam.gserviceaccount.com"
CLOUD_STATE=$HOME/.jarvis/fitglue-cloud-run-ingress.json
PARKRUN_ENV=$HOME/.jarvis/parkrun-agent.env
PARKRUN_AGENT=${PARKRUN_AGENT:-$HOME/jarvis/scripts/parkrun_agent.py}
PARKRUN_PYTHON=${PARKRUN_PYTHON:-$HOME/jarvis/.venv/bin/python}
UNIT_DIR=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user

SERVICES=(api-webhook api-client user pipeline activity destination)
CLOUD_SERVICES=(api-webhook api-client user pipeline activity destination)
TIMERS=(fitglue-roundup-weekly fitglue-roundup-monthly fitglue-roundup-yearly fitglue-parkrun-check)
# Required keys in ~/.jarvis/fitglue.env (deploy/box/fitglue.env.example). Values
# are checked for presence only and never echoed.
REQUIRED_ENV=(FITGLUE_API_TOKEN FITGLUE_OWNER_UID PUBSUB_PUSH_AUDIENCE OAUTH_STATE_SECRET
  FITGLUE_PUBLIC_BASE_URL GEMINI_API_KEY STRAVA_CLIENT_ID STRAVA_CLIENT_SECRET
  STRAVA_WEBHOOK_VERIFY_TOKEN FITBIT_CLIENT_ID FITBIT_CLIENT_SECRET FITBIT_OAUTH_CLIENT_SECRET
  FITBIT_SUBSCRIBER_VERIFICATION_TOKEN)
OPTIONAL_ENV=(WEB_URL TRAININGPEAKS_CLIENT_ID TRAININGPEAKS_CLIENT_SECRET GITHUB_CLIENT_ID
  GITHUB_CLIENT_SECRET GITHUB_WEBHOOK_SECRET SPOTIFY_CLIENT_ID SPOTIFY_CLIENT_SECRET SENTRY_DSN
  SITE_EXPORT_HOOK_URL)
# Public push paths (Caddyfile) and what each maps to.
PUSH_PATHS=(pipeline/raw pipeline/run pipeline/enriched pipeline/parkrun-check activity/roundup destination/)
# Pub/Sub push subscriptions (terraform/pubsub.tf) -> expected endpoint.
declare -A SUB_ENDPOINT=(
  [sub-pipeline-raw]="$PUBSUB_AUDIENCE/pipeline/raw"
  [sub-pipeline-run]="$PUBSUB_AUDIENCE/pipeline/run"
  [sub-pipeline-enriched]="$PUBSUB_AUDIENCE/pipeline/enriched"
  [sub-enrichment-lag]="$PUBSUB_AUDIENCE/pipeline/run"
  [sub-parkrun-results-check]="$PUBSUB_AUDIENCE/pipeline/parkrun-check"
  [sub-roundup-trigger]="$PUBSUB_AUDIENCE/activity/roundup"
  [sub-destination-upload]="$PUBSUB_AUDIENCE/destination/"
)

pass=0; fail=0; warn=0
ok()   { pass=$((pass + 1)); printf '  ok    %s\n' "$*"; }
bad()  { fail=$((fail + 1)); printf '  FAIL  %s\n' "$*"; }
note() { warn=$((warn + 1)); printf '  warn  %s\n' "$*"; }
info() { printf '  ..    %s\n' "$*"; }
section() { printf '\n== %s\n' "$*"; }
summary() {
  printf '\n%s: %d ok, %d warn, %d FAIL\n' "$1" "$pass" "$warn" "$fail"
  pass=0; warn=0; fail=0
}
confirm() {
  printf '\nType YES to continue, anything else aborts: '
  local a; read -r a
  [ "$a" = "YES" ] || { echo "aborted"; exit 1; }
}
have() { command -v "$1" >/dev/null 2>&1; }

# envget KEY -> value from ENVFILE (systemd format: KEY=value, no quotes). Never print it.
envget() { grep -s "^$1=" "$ENVFILE" | tail -1 | cut -d= -f2-; }

http_code() {  # http_code METHOD URL [curl args...]
  local m=$1 u=$2; shift 2
  curl -s -o /dev/null -w '%{http_code}' -m 20 -X "$m" "$@" "$u" 2>/dev/null || echo "000"
}

port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

health_url() {  # the loopback URL that says a service is up ("" = TCP only)
  case $1 in
    api-webhook) echo "http://127.0.0.1:8101/api/webhooks/health" ;;
    api-client)  echo "http://127.0.0.1:8102/health" ;;
    pipeline)    echo "http://127.0.0.1:8104/health" ;;
    activity)    echo "http://127.0.0.1:8105/health" ;;
    *)           echo "" ;;
  esac
}
port_of() {
  case $1 in
    api-webhook) echo 8101 ;; api-client) echo 8102 ;; user) echo 8103 ;;
    pipeline) echo 8104 ;; activity) echo 8105 ;; destination) echo 8106 ;;
  esac
}

# ── --check ──────────────────────────────────────────────────────────────────
do_check() {
  section "Funnel"
  if have tailscale; then
    local fs; fs=$(tailscale funnel status 2>&1 || true)
    if grep -q "Funnel on" <<<"$fs" && grep -q "127.0.0.1:8100" <<<"$fs"; then
      ok "tailscale funnel: https://$HOST -> http://127.0.0.1:8100"
    else
      bad "tailscale funnel is not mapping 443 -> 8100 (tailscale funnel --bg --yes 8100; see deploy/box/funnel.md)"
    fi
  else
    bad "tailscale CLI not found"
  fi

  section "Caddy"
  if systemctl --user is-active --quiet fitglue-caddy.service; then
    ok "fitglue-caddy.service active"
  else
    bad "fitglue-caddy.service not active (systemctl --user enable --now fitglue-caddy)"
  fi
  local c
  c=$(http_code GET "http://127.0.0.1:8100/")
  [ "$c" = 404 ] && ok "127.0.0.1:8100/ -> 404 (Caddy answering, nothing else exposed)" || bad "127.0.0.1:8100/ -> $c (expected 404)"
  c=$(http_code GET "https://$HOST/")
  [ "$c" = 404 ] && ok "https://$HOST/ -> 404 (Funnel -> Caddy end to end)" || bad "https://$HOST/ -> $c (expected 404; 000 = Funnel/TLS not reachable)"
  if [ -f "$CHECKOUT/deploy/box/caddy/Caddyfile" ] && have caddy; then
    caddy validate --config "$CHECKOUT/deploy/box/caddy/Caddyfile" >/dev/null 2>&1 && ok "Caddyfile validates" || bad "Caddyfile does not validate"
  fi

  section "Units (symlinks into $CHECKOUT/deploy/box)"
  local u
  for u in "${SERVICES[@]/#/fitglue-}"; do
    check_unit "$u.service"
  done
  for u in "${TIMERS[@]}"; do
    check_unit "$u.timer"; check_unit "$u.service"
  done
  check_unit fitglue.target
  if [ -e "$UNIT_DIR/fitglue-api-public.service" ] || [ -L "$UNIT_DIR/fitglue-api-public.service" ]; then
    bad "stale fitglue-api-public.service still linked (re-run deploy/box/install.sh)"
  else
    ok "no stale fitglue-api-public.service"
  fi
  local st; st=$(systemctl --user is-enabled fitglue.target 2>/dev/null || true)
  info "fitglue.target is-enabled: ${st:-unknown}; active services: $(systemctl --user list-units --plain --no-legend 'fitglue-*.service' 2>/dev/null | awk '$3=="active"{print $1}' | tr '\n' ' ')"

  section "Binaries ($BIN)"
  local b
  for b in "${SERVICES[@]}" fitgluectl; do
    [ -x "$BIN/$b" ] && ok "$b" || bad "$b missing (make build-box in $CHECKOUT)"
  done

  section "Environment ($ENVFILE)"
  if [ ! -f "$ENVFILE" ]; then
    bad "$ENVFILE missing (F3.5 writes it; see deploy/box/fitglue.env.example)"
  else
    local mode; mode=$(stat -c %a "$ENVFILE")
    [ "$mode" = 600 ] && ok "mode 600" || bad "mode $mode (chmod 600 $ENVFILE)"
    local k missing=()
    for k in "${REQUIRED_ENV[@]}"; do
      [ -n "$(envget "$k")" ] || missing+=("$k")
    done
    if [ ${#missing[@]} -eq 0 ]; then
      ok "all ${#REQUIRED_ENV[@]} keys the cut-over needs are set"
    else
      bad "keys empty/missing: ${missing[*]}"
    fi
    # F3.5's full comparison against fitglue.env.example (names only).
    if [ -x "$CHECKOUT/deploy/box/env-check.sh" ]; then
      if "$CHECKOUT/deploy/box/env-check.sh" --quiet "$ENVFILE" >/dev/null 2>&1; then
        ok "env-check.sh: every required key in fitglue.env.example is set"
      else
        bad "env-check.sh reports a missing/empty required key: deploy/box/env-check.sh"
      fi
    fi
    local opt_missing=()
    for k in "${OPTIONAL_ENV[@]}"; do
      [ -n "$(envget "$k")" ] || opt_missing+=("$k")
    done
    [ ${#opt_missing[@]} -eq 0 ] || note "optional keys unset: ${opt_missing[*]}"
    local aud; aud=$(envget PUBSUB_PUSH_AUDIENCE)
    if [ "$aud" = "$PUBSUB_AUDIENCE" ]; then
      ok "PUBSUB_PUSH_AUDIENCE == $PUBSUB_AUDIENCE (terraform/pubsub.tf)"
    else
      bad "PUBSUB_PUSH_AUDIENCE is '${aud:-<unset>}', terraform signs for '$PUBSUB_AUDIENCE' — every push would be 403"
    fi
    local pub; pub=$(envget FITGLUE_PUBLIC_BASE_URL)
    case $pub in
      "https://$HOST"*) ok "FITGLUE_PUBLIC_BASE_URL is under https://$HOST" ;;
      "") ;;  # already reported as missing
      *) note "FITGLUE_PUBLIC_BASE_URL is not under https://$HOST — OAuth callbacks will not come back here (docs/guides/box-ingress.md)" ;;
    esac
    grep -q '^export \|^[^#=]*="' "$ENVFILE" && note "env file has 'export' or quoted values; systemd reads KEY=value literally" || true
  fi

  section "Service-account key ($SA_KEY)"
  if [ ! -f "$SA_KEY" ]; then
    bad "missing (F3.2)"
  else
    local mode; mode=$(stat -c %a "$SA_KEY")
    [ "$mode" = 600 ] && ok "mode 600" || bad "mode $mode (chmod 600 $SA_KEY)"
    local email
    email=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("client_email",""))' "$SA_KEY" 2>/dev/null || true)
    [ "$email" = "$SA_EMAIL" ] && ok "client_email $SA_EMAIL" || bad "client_email is '${email:-unreadable}', expected $SA_EMAIL"
  fi

  section "Firestore read as $SA_EMAIL"
  if [ -x "$BIN/fitgluectl" ] && [ -f "$SA_KEY" ] && [ -n "$(envget FITGLUE_OWNER_UID)" ]; then
    local out
    if out=$(GOOGLE_APPLICATION_CREDENTIALS="$SA_KEY" PROJECT_ID="$PROJECT" FITGLUE_OWNER_UID="$(envget FITGLUE_OWNER_UID)" \
             "$BIN/fitgluectl" connections 2>&1); then
      ok "fitgluectl connections read the owner document ($(wc -l <<<"$out") lines)"
    else
      case $out in
        *"account not found"*|*"Unauthenticated"*)
          bad "the key is not accepted: 'account not found' means $SA_EMAIL was deleted or recreated (a key is bound to the SA's unique id) — gcloud iam service-accounts list, then re-mint ~/.jarvis/fitglue-box-sa.json (F3.2)" ;;
        *) bad "fitgluectl connections failed: $(tail -1 <<<"$out")" ;;
      esac
    fi
  else
    bad "skipped (needs bin/fitgluectl, the key and FITGLUE_OWNER_UID)"
  fi

  section "Cloud side (read-only)"
  if have gcloud; then
    local acct; acct=$(gcloud config get-value account 2>/dev/null || true)
    [ -n "$acct" ] && ok "gcloud account set (needed for --strava/--scale-down-cloud)" || note "gcloud has no active account; --strava and --scale-down-cloud will fail"
    local onbox=0 oncloud=0 line
    while IFS=$'\t' read -r name ep; do
      [ -n "$name" ] || continue
      case $ep in "$PUBSUB_AUDIENCE"/*) onbox=$((onbox + 1)) ;; *) oncloud=$((oncloud + 1)) ;; esac
    done < <(gcloud pubsub subscriptions list --project "$PROJECT" --filter='pushConfig.pushEndpoint:*' \
              --format='value(name.basename(),pushConfig.pushEndpoint)' 2>/dev/null || true)
    info "push subscriptions: $onbox at the box, $oncloud elsewhere (terraform PR applied = 7 at the box; see --pubsub)"
    if [ -f "$CLOUD_STATE" ]; then
      info "Cloud Run ingress record exists ($CLOUD_STATE) — --scale-down-cloud has run; --rollback restores it"
    fi
  else
    note "gcloud not found"
  fi
  summary "--check"
}

check_unit() {
  local u=$1 link="$UNIT_DIR/$1"
  if [ ! -L "$link" ]; then
    bad "$u not linked in $UNIT_DIR (deploy/box/install.sh)"; return
  fi
  case "$(readlink "$link")" in
    "$CHECKOUT"/deploy/box/*) ;;
    *) note "$u links to $(readlink "$link"), not $CHECKOUT/deploy/box" ;;
  esac
  if [ "$(systemctl --user show -p LoadState --value "$u" 2>/dev/null)" = loaded ]; then
    ok "$u"
  else
    bad "$u not loaded: $(systemctl --user show -p LoadError --value "$u" 2>/dev/null)"
  fi
}

# ── --start ──────────────────────────────────────────────────────────────────
do_start() {
  section "Start fitglue.target"
  systemctl --user enable --now fitglue.target
  local s url deadline
  for s in "${SERVICES[@]}"; do
    url=$(health_url "$s")
    deadline=$((SECONDS + 60))
    while :; do
      if [ -n "$url" ]; then
        [ "$(http_code GET "$url")" = 200 ] && { ok "$s: $url -> 200"; break; }
      else
        port_open "$(port_of "$s")" && { ok "$s: port $(port_of "$s") open"; break; }
      fi
      if [ $SECONDS -ge $deadline ]; then
        bad "$s not healthy after 60s: $(systemctl --user is-active "fitglue-$s.service" 2>&1); journalctl --user -u fitglue-$s -n 20"
        break
      fi
      sleep 2
    done
  done
  section "Timers"
  systemctl --user list-timers --all --no-pager 'fitglue-*' || true
  local t
  for t in "${TIMERS[@]}"; do
    systemctl --user is-active --quiet "$t.timer" && ok "$t.timer active" || bad "$t.timer not active"
  done
  section "Through Funnel"
  local c; c=$(http_code GET "$BASE/webhook/health")
  [ "$c" = 200 ] && ok "$BASE/webhook/health -> 200" || bad "$BASE/webhook/health -> $c"
  summary "--start"
}

# ── --pubsub ─────────────────────────────────────────────────────────────────
do_pubsub() {
  section "Pub/Sub push subscriptions (terraform/pubsub.tf)"
  cat <<EOF
  The subscriptions are moved by terraform, not by this script: merging the F3.4
  PR makes CI apply it (one run: 7 subscriptions updated in place, 4 Cloud
  Scheduler jobs destroyed, 3 run.invoker self-grants removed). Nothing to do
  here but confirm the result.
EOF
  if have gcloud; then
    local name ep sa aud
    while IFS=$'\t' read -r name ep sa aud; do
      [ -n "$name" ] || continue
      [ -n "${SUB_ENDPOINT[$name]+x}" ] || { info "$name -> $ep (not one of ours)"; continue; }
      if [ "$ep" = "${SUB_ENDPOINT[$name]}" ] && [ "$aud" = "$PUBSUB_AUDIENCE" ]; then
        ok "$name -> $ep (audience ok, signs as ${sa%%@*})"
      elif [ "$ep" = "${SUB_ENDPOINT[$name]}" ]; then
        bad "$name -> $ep but audience is '${aud:-<default>}' (expected $PUBSUB_AUDIENCE)"
      else
        bad "$name -> $ep (expected ${SUB_ENDPOINT[$name]}; terraform not applied yet?)"
      fi
    done < <(gcloud pubsub subscriptions list --project "$PROJECT" --filter='pushConfig.pushEndpoint:*' \
              --format='value(name.basename(),pushConfig.pushEndpoint,pushConfig.oidcToken.serviceAccountEmail,pushConfig.oidcToken.audience)' 2>/dev/null || true)
  else
    note "gcloud not found; cannot list subscriptions"
  fi

  section "Public push paths reject unauthenticated POSTs"
  local p c
  for p in "${PUSH_PATHS[@]}"; do
    c=$(http_code POST "$BASE/pubsub/$p" -H 'Content-Type: application/json' -d '{"message":{"data":"e30="}}')
    case $c in
      401|403) ok "$BASE/pubsub/$p -> $c (OIDC check live)" ;;
      404) bad "$BASE/pubsub/$p -> 404 (Caddy route missing)" ;;
      502|503) bad "$BASE/pubsub/$p -> $c (service behind it is down)" ;;
      000) bad "$BASE/pubsub/$p unreachable (Funnel?)" ;;
      *) bad "$BASE/pubsub/$p -> $c: the service accepted an unauthenticated push — PUBSUB_PUSH_AUDIENCE is unset in its environment; stop fitglue.target" ;;
    esac
  done
  summary "--pubsub"
}

# ── --parkrun ────────────────────────────────────────────────────────────────
do_parkrun() {
  section "Box parkrun agent -> 127.0.0.1:8104 (F3.4e)"
  local want
  want=$(printf 'FITGLUE_PIPELINE_URL=http://127.0.0.1:8104\nPARKRUN_AGENT_SA_KEY=%s\n' "$SA_KEY")
  if [ -f "$PARKRUN_ENV" ] && [ "$(cat "$PARKRUN_ENV")" = "$want" ]; then
    ok "$PARKRUN_ENV already as wanted"
  else
    (umask 077; printf '%s\n' "$want" > "$PARKRUN_ENV")
    ok "wrote $PARKRUN_ENV"
  fi
  cat <<EOF
  jarvis-parkrun-agent.service reads that file (EnvironmentFile=-, jarvis repo
  F3.4 branch). If the installed unit predates that line:
     jarvis-privileged install-units && sudo systemctl daemon-reload
  The timer itself is unchanged (Sat/Sun/Mon every 30 min).
EOF
  if [ -x "$PARKRUN_PYTHON" ] && [ -f "$PARKRUN_AGENT" ]; then
    if port_open 8104; then
      info "dry run against the box pipeline (lists pending inputs, posts nothing):"
      if FITGLUE_PIPELINE_URL=http://127.0.0.1:8104 PARKRUN_AGENT_SA_KEY="$SA_KEY" "$PARKRUN_PYTHON" "$PARKRUN_AGENT" --dry-run; then
        ok "agent authenticated to 127.0.0.1:8104 with the fitglue-box key"
      else
        bad "agent dry run failed against the box pipeline (journal: fitglue-pipeline 'identity verification failed' = audience/key mismatch)"
      fi
    else
      note "pipeline not listening on 8104; run --start first, then re-run --parkrun for the dry run"
    fi
  else
    note "agent or its venv not found ($PARKRUN_AGENT, $PARKRUN_PYTHON)"
  fi
  summary "--parkrun"
}

# ── --strava ─────────────────────────────────────────────────────────────────
secret() { gcloud secrets versions access latest --secret "$1" --project "$PROJECT" 2>/dev/null; }

do_strava() {
  section "Strava webhook subscription -> $BASE/webhook/strava"
  have gcloud || { bad "gcloud needed to read strava-client-id/-secret/-verify-token"; summary "--strava"; return; }
  local cid csec vtok
  cid=$(envget STRAVA_CLIENT_ID); csec=$(envget STRAVA_CLIENT_SECRET); vtok=$(envget STRAVA_WEBHOOK_VERIFY_TOKEN)
  [ -n "$cid" ]  || cid=$(secret strava-client-id)
  [ -n "$csec" ] || csec=$(secret strava-client-secret)
  [ -n "$vtok" ] || vtok=$(secret strava-verify-token)
  if [ -z "$cid" ] || [ -z "$csec" ] || [ -z "$vtok" ]; then
    bad "could not read the Strava client id / secret / verify token (fitglue.env or Secret Manager)"; summary "--strava"; return
  fi
  local target="$BASE/webhook/strava"
  local list; list=$(curl -s -m 20 -G https://www.strava.com/api/v3/push_subscriptions -d "client_id=$cid" -d "client_secret=$csec")
  local id cb
  id=$(python3 -c 'import json,sys; l=json.load(sys.stdin); print(l[0]["id"] if l else "")' <<<"$list" 2>/dev/null || true)
  cb=$(python3 -c 'import json,sys; l=json.load(sys.stdin); print(l[0]["callback_url"] if l else "")' <<<"$list" 2>/dev/null || true)
  if [ -z "$id" ]; then
    info "no subscription exists for this app"
  else
    info "current subscription id $id -> $cb"
  fi
  if [ "$cb" = "$target" ]; then
    ok "already points at $target"; summary "--strava"; return
  fi
  # Strava verifies the new callback with a GET before accepting the create;
  # prove the box answers it first (same request, our own verify token).
  local chal; chal="cutover-$RANDOM"
  local resp; resp=$(curl -s -m 20 -G "$target" -d hub.mode=subscribe -d "hub.verify_token=$vtok" -d "hub.challenge=$chal")
  if grep -q "\"hub.challenge\":\"$chal\"" <<<"$resp"; then
    ok "$target answers the verification handshake"
  else
    bad "$target did not echo hub.challenge (is api-webhook up and STRAVA_WEBHOOK_VERIFY_TOKEN the same on both sides?): ${resp:0:120}"
    summary "--strava"; return
  fi
  cat <<EOF
  Plan (Strava has no update call, so delete then create; events in the gap are lost,
  usually a second or two):
    DELETE https://www.strava.com/api/v3/push_subscriptions/${id:-<none>}
    POST   https://www.strava.com/api/v3/push_subscriptions callback_url=$target
EOF
  confirm
  if [ -n "$id" ]; then
    local dc; dc=$(http_code DELETE "https://www.strava.com/api/v3/push_subscriptions/$id?client_id=$cid&client_secret=$csec")
    [ "$dc" = 204 ] && ok "deleted subscription $id" || { bad "delete returned $dc"; summary "--strava"; return; }
  fi
  local created
  created=$(curl -s -m 30 -X POST https://www.strava.com/api/v3/push_subscriptions \
    -F "client_id=$cid" -F "client_secret=$csec" -F "callback_url=$target" -F "verify_token=$vtok")
  local nid; nid=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))' <<<"$created" 2>/dev/null || true)
  if [ -n "$nid" ]; then
    ok "created subscription $nid -> $target"
  else
    bad "create failed: ${created:0:200}"
  fi
  summary "--strava"
}

# ── --scale-down-cloud / --rollback ──────────────────────────────────────────
cloud_ingress() { gcloud run services describe "$1" --project "$PROJECT" --region "$REGION" --format='value(metadata.annotations["run.googleapis.com/ingress"])' 2>/dev/null; }

do_scale_down() {
  section "Cloud Run -> ingress internal (the 'scaled to zero' proof)"
  have gcloud || { bad "gcloud needed"; summary "--scale-down-cloud"; return; }
  cat <<EOF
  Cloud Run rejects max-instances=0 ("maxScale must be a positive integer",
  verified with 'gcloud run services replace --dry-run'), so the equivalent,
  reversible step is ingress=internal on every service: nothing from the
  internet (Strava, Fitbit, Hevy, a browser) reaches the cloud deployment, and
  the Pub/Sub subscriptions already push to the box. The merged terraform also
  removed the push self-invoker grants, so a stale push at a Cloud Run URL is
  403 either way. terraform still declares INGRESS_TRAFFIC_ALL — the next CI
  apply puts it back, which is fine: F4 deletes the services.
EOF
  local s cur
  if [ ! -f "$CLOUD_STATE" ]; then
    local rec="{"
    for s in "${CLOUD_SERVICES[@]}"; do
      cur=$(cloud_ingress "$s"); [ -n "$cur" ] || cur="unknown"
      rec+="\"$s\":\"$cur\","
    done
    (umask 077; printf '%s}\n' "${rec%,}" > "$CLOUD_STATE")
    ok "recorded current ingress to $CLOUD_STATE"
  else
    info "ingress record already exists at $CLOUD_STATE (kept; --rollback uses it)"
  fi
  local todo=()
  for s in "${CLOUD_SERVICES[@]}"; do
    cur=$(cloud_ingress "$s")
    if [ "$cur" = internal ]; then ok "$s already internal"; else todo+=("$s"); info "$s: $cur -> internal"; fi
  done
  [ ${#todo[@]} -gt 0 ] || { summary "--scale-down-cloud"; return; }
  confirm
  for s in "${todo[@]}"; do
    if gcloud run services update "$s" --ingress=internal --project "$PROJECT" --region "$REGION" --quiet >/dev/null 2>&1; then
      ok "$s ingress=internal"
    else
      bad "$s update failed"
    fi
  done
  cat <<EOF
  Proof after the next Strava activity:
    journalctl --user -u fitglue-api-webhook --since -2h | grep -i strava      # arrived at the box
    gcloud logging read 'resource.type="cloud_run_revision" AND httpRequest.requestUrl:"/"' \\
      --project $PROJECT --freshness=2h --limit 5 --format='value(timestamp,httpRequest.requestUrl,httpRequest.status)'
                                                                               # nothing on Cloud Run
EOF
  summary "--scale-down-cloud"
}

do_rollback() {
  section "Cloud Run ingress rollback"
  have gcloud || { bad "gcloud needed"; summary "--rollback"; return; }
  [ -f "$CLOUD_STATE" ] || { bad "no record at $CLOUD_STATE; nothing to roll back"; summary "--rollback"; return; }
  local s want cur todo=()
  for s in "${CLOUD_SERVICES[@]}"; do
    want=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get(sys.argv[2],""))' "$CLOUD_STATE" "$s")
    cur=$(cloud_ingress "$s")
    case $want in
      ""|unknown) note "$s: no recorded value, leaving $cur" ;;
      "$cur") ok "$s already $cur" ;;
      *) todo+=("$s=$want"); info "$s: $cur -> $want" ;;
    esac
  done
  [ ${#todo[@]} -gt 0 ] || { summary "--rollback"; return; }
  confirm
  local pair
  for pair in "${todo[@]}"; do
    s=${pair%%=*}; want=${pair#*=}
    if gcloud run services update "$s" --ingress="$want" --project "$PROJECT" --region "$REGION" --quiet >/dev/null 2>&1; then
      ok "$s ingress=$want"
    else
      bad "$s update failed"
    fi
  done
  summary "--rollback"
}

usage() { sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'; }

[ $# -gt 0 ] || { usage; exit 2; }
for arg in "$@"; do
  case $arg in
    --check) do_check ;;
    --start) do_start ;;
    --pubsub) do_pubsub ;;
    --parkrun) do_parkrun ;;
    --strava) do_strava ;;
    --scale-down-cloud) do_scale_down ;;
    --rollback) do_rollback ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown flag: $arg" >&2; usage >&2; exit 2 ;;
  esac
done
