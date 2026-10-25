# Public ingress: Caddy + Tailscale Funnel (F3.3)

The box has no public IP. Tailscale Funnel exposes **one** port of the node
`jarvis.tail3065f7.ts.net` to the internet — 443, TLS terminated by Tailscale
with a Let's Encrypt cert it manages — and forwards plain HTTP to Caddy on
`127.0.0.1:8100`. Caddy routes `/fg/*` to FitGlue's services and answers an
empty 404 to everything else.

```
internet ──TLS──▶ Tailscale relay ──▶ tailscaled (443) ──http──▶ caddy 127.0.0.1:8100 ──▶ 810x services
```

Public base URL for F3.4 (Pub/Sub push endpoints, Strava/Fitbit/Hevy webhooks,
OAuth callbacks): **`https://jarvis.tail3065f7.ts.net/fg/...`**

## Routes

| Public path | Backend | Backend path | Notes |
|---|---|---|---|
| `/fg/webhook/{strava,fitbit,hevy,health}` | api-webhook 8101 | `/api/webhooks/{…}` | GET verification + POST events |
| `/fg/pubsub/pipeline/{raw,run,enriched,parkrun-check}` | pipeline 8104 | `/pubsub/{…}` | pipeline's `/internal/*` is **not** routed |
| `/fg/pubsub/activity/roundup` | activity 8105 | `/pubsub/roundup` | |
| `/fg/pubsub/destination/` | destination 8106 | `/` | destination's push handler is mounted at `/`; the slash-less form is accepted too |
| `/fg/oauth/{provider}/callback` | api-client 8102 | `/api/v2/oauth/{provider}/callback` | `fitgluectl connect` |
| anything else | — | — | `404`, empty body |

Request body limit 10 MB; read-header 10s, read-body 30s, write 60s, idle 2m.
Access log: `~/.jarvis/logs/caddy-access.log` (JSON, 20 MiB × 5 rolls, 30 days).

The `API_URL` the api-client service uses to build OAuth redirect URIs appends
`/api/v2/oauth/<provider>/callback`, which does not line up with `/fg/oauth/…`.
F3.4/F2.3 must set the public callback to
`https://jarvis.tail3065f7.ts.net/fg/oauth/<provider>/callback` (a separate
`PUBLIC_OAUTH_BASE`-style value, or route-aware `API_URL` handling) — Caddy
does not expose `/api/v2/*` wholesale on purpose.

## Install (no sudo)

```sh
deploy/box/caddy/install-caddy.sh          # ~/.local/bin/caddy, pinned, sha512-verified
install -D -m 0644 deploy/box/fitglue-caddy.service ~/.config/systemd/user/fitglue-caddy.service
systemctl --user daemon-reload
systemctl --user enable --now fitglue-caddy
caddy validate --config deploy/box/caddy/Caddyfile   # before any change
systemctl --user reload fitglue-caddy                # graceful config reload
```

`loginctl show-user jarvis -p Linger` is `yes`, so the unit survives logout and
starts at boot.

## Funnel

Status: `tailscale funnel status` (and `tailscale serve status` — Funnel is a
flag on the serve config).

Turn on, persisted across reboots (`--bg` writes it into tailscaled's serve
config; `--yes` skips the interactive confirmation):

```sh
tailscale funnel --bg --yes 8100
```

This maps **only** `https://jarvis.tail3065f7.ts.net:443` → `http://127.0.0.1:8100`.
Do not add `--set-path`, other ports, or `tailscale serve` entries for 8787/8788/8790:
anything added to the serve config with Funnel on becomes public.

Turn off:

```sh
tailscale funnel --https=443 off      # just this mapping
tailscale funnel reset                # wipe the whole serve/funnel config
```

### Tailnet prerequisite (James, admin console)

Funnel must be allowed by the tailnet policy: the node needs the `funnel` node
attribute. As of 2026-10-25 it is **not** enabled; the CLI returns:

```
Funnel is not enabled on your tailnet.
To enable, visit:

         https://login.tailscale.com/f/funnel?node=ncpfNPDeg321CNTRL
```

One-line instruction: open
<https://login.tailscale.com/f/funnel?node=ncpfNPDeg321CNTRL> as the tailnet
admin and accept — it adds `"nodeAttrs": [{"target": ["autogroup:member"], "attr": ["funnel"]}]`
to the ACL policy (equivalently, add that block by hand under Access controls).
Then on the box: `tailscale funnel --bg --yes 8100`, and
`curl -s https://jarvis.tail3065f7.ts.net/fg/webhook/health` from any
off-tailnet machine must return `{"status": "ok"}` once F3.1's api-webhook is up.

Tailnet HTTPS (MagicDNS + HTTPS certificates) is already on — the voicebox mints
its cert with `tailscale cert` — and `tailscale set --operator=jarvis` is done,
so none of the above needs sudo.

## Security posture

- **Exposed:** only `/fg/*` as listed above. Caddy has no default site, no file
  server, no admin TCP port (admin API is a unix socket under
  `$XDG_RUNTIME_DIR`, mode 0600), and returns an empty 404 for everything else.
- **Not exposed:** the home screen (8790), the dashboard (8787), the voicebox
  (8788), the gateway, api-public (8107), user (8103), and pipeline's
  `/internal/*` endpoints. Funnel forwards 443 to 8100 and nothing else; those
  services bind to loopback/tailnet addresses and Tailscale remains their auth.
  Caddy itself binds `127.0.0.1:8100` only.
- **Pub/Sub pushes** still carry a Google-signed OIDC token and the services keep
  their existing audience/issuer check; Caddy adds no auth and removes none.
  Unauthenticated POSTs to `/fg/pubsub/*` reach the service and are rejected
  there, exactly as on Cloud Run (which was also public-ingress).
- **Webhooks** keep their own checks: Strava `hub.verify_token` on subscription
  + event-shape validation, Fitbit subscriber verification code + `X-Fitbit-Signature`
  HMAC, Hevy per-user key. Nothing in Caddy relaxes them.
- **OAuth callbacks** are state-bound (`OAUTH_STATE_SECRET`); the callback
  route only exchanges the code for a token and writes the integration.
- **Tokens and secrets:** the Caddyfile holds none, and nothing is injected
  in transit. The JSON access log records method, URI, status, size, timing,
  `client_ip`/`remote_ip` and request headers, with `Authorization`, `Cookie`
  and `Proxy-Authorization` written as `REDACTED` (Caddy's default, and
  restated as an explicit `format filter` in the Caddyfile). Bodies are never
  logged. Verified: a request carrying `Authorization: Bearer <value>` leaves
  no trace of `<value>` in `~/.jarvis/logs/caddy-access.log`. The log file is
  mode 0600 under `~/.jarvis/logs`, which restic already covers.
- **Rate/size:** 10 MB body cap, header/body/write timeouts, so a slow or
  oversized client cannot hold a worker; Funnel also rate-limits inbound at
  Tailscale's edge.
- **Client IP:** tailscaled sets `X-Forwarded-For`; Caddy trusts only
  `127.0.0.0/8` as a proxy, so a spoofed `X-Forwarded-For` from the internet is
  replaced by the Funnel-observed address before it reaches the services.

## Verify

```sh
caddy validate --config deploy/box/caddy/Caddyfile
systemctl --user status fitglue-caddy
curl -si http://127.0.0.1:8100/                                  # 404, empty
curl -si http://127.0.0.1:8100/fg/webhook/health                 # 200 from 8101
tail -1 ~/.jarvis/logs/caddy-access.log | python3 -m json.tool   # the request, JSON
tailscale funnel status
# Off-tailnet (phone on mobile data, or any public HTTP checker):
curl -si https://jarvis.tail3065f7.ts.net/fg/webhook/health      # 200
curl -si https://jarvis.tail3065f7.ts.net/                       # 404
```
