# Box ingress: Caddy + Tailscale Funnel

How FitGlue's webhooks, Pub/Sub pushes and OAuth callbacks reach the box now that
the services run there instead of on Cloud Run (rethink F3). The authoritative
operational notes — routes table, install, Funnel on/off, security posture — are
in [`deploy/box/funnel.md`](../../deploy/box/funnel.md); this page is the
orientation.

## Shape

```
internet ──TLS──▶ Tailscale Funnel (jarvis.tail3065f7.ts.net:443)
                     └─http──▶ caddy 127.0.0.1:8100  (deploy/box/caddy/Caddyfile)
                                   ├─ /fg/webhook/*            → api-webhook 8101  /api/webhooks/*
                                   ├─ /fg/pubsub/pipeline/*    → pipeline    8104  /pubsub/*
                                   ├─ /fg/pubsub/activity/*    → activity    8105  /pubsub/*
                                   ├─ /fg/pubsub/destination/  → destination 8106  /
                                   ├─ /fg/oauth/*              → api-client  8102  /api/v2/oauth/*
                                   └─ *                        → 404, empty body
```

- Tailscale owns TLS (Let's Encrypt via the tailnet's HTTPS feature); Caddy
  never sees a certificate and runs with `auto_https off`.
- Caddy binds loopback only and is a systemd **user** unit,
  `fitglue-caddy.service` (`deploy/box/fitglue-caddy.service`), alongside the
  `fitglue-<svc>.service` units from F3.1. No sudo anywhere.
- Funnel is the only public door. The home screen (8790), dashboard (8787),
  voicebox (8788), api-public (8107) and user (8103) stay tailnet-only.

## Public URLs (what F3.4 points the inputs at)

| Input | URL |
|---|---|
| Strava webhook callback | `https://jarvis.tail3065f7.ts.net/fg/webhook/strava` |
| Fitbit subscriber endpoint | `https://jarvis.tail3065f7.ts.net/fg/webhook/fitbit` |
| Hevy webhook | `https://jarvis.tail3065f7.ts.net/fg/webhook/hevy` |
| Pub/Sub push: raw / run / enriched / parkrun-check | `https://jarvis.tail3065f7.ts.net/fg/pubsub/pipeline/{raw,run,enriched,parkrun-check}` |
| Pub/Sub push: roundup | `https://jarvis.tail3065f7.ts.net/fg/pubsub/activity/roundup` |
| Pub/Sub push: destination | `https://jarvis.tail3065f7.ts.net/fg/pubsub/destination/` |
| OAuth callback (`fitgluectl connect`) | `https://jarvis.tail3065f7.ts.net/fg/oauth/{provider}/callback` |
| Liveness from outside | `https://jarvis.tail3065f7.ts.net/fg/webhook/health` |

Pub/Sub push subscriptions keep their OIDC token and Caddy does not look at
it; the services do, in-process, since F3.1 (`PUBSUB_PUSH_AUDIENCE`). The
audience must therefore be set explicitly on every push subscription to the
one value in `~/.jarvis/fitglue.env` — Pub/Sub's default (the push URL)
differs per subscription and would not match. See
`docs/guides/box-deploy.md`, "Pub/Sub push authentication".

## Day to day

```sh
systemctl --user status fitglue-caddy
caddy validate --config deploy/box/caddy/Caddyfile && systemctl --user reload fitglue-caddy
tail -f ~/.jarvis/logs/caddy-access.log | jq -c '{s:.status, m:.request.method, u:.request.uri, ip:.request.client_ip}'
tailscale funnel status
```

A 404 with an empty body from the public hostname means Caddy answered and the
path is not routed; a 502 means Caddy answered and the service behind that path
is down (`systemctl --user status 'fitglue-*'`); a TLS or connection error means
Funnel is off (`deploy/box/funnel.md` → Funnel).

## Changing routes

Edit `deploy/box/caddy/Caddyfile`, run `caddy validate`, open a PR. Keep the
rule: public paths live under `/fg/`, each maps to one loopback port, nothing
proxies a service wholesale (pipeline's `/internal/*`, api-client's
`/api/v2/*` beyond the OAuth callback, api-public, user are deliberately not
reachable). Adding a `tailscale serve` or `funnel` entry for any other port
makes it public — don't.
