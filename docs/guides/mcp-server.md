# FitGlue MCP Server (stdio)

`cmd/mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server
that exposes a read-only FitGlue tool surface to MCP clients (Claude Code,
Claude Desktop, MCP Inspector). It wraps the `api-client` HTTP gateway and
authenticates with its static bearer token — FitGlue is single-user, so the
token *is* the identity.

This is **V0 of the connector roadmap**: a local stdio binary for development
and personal use. The remote (Streamable HTTP + OAuth) server that lets other
users connect from claude.ai will build on the same tool surface.

## Build

```bash
make build              # builds bin/fitglue-mcp along with the other tools
# or directly:
cd src/go && go build -o ../../bin/fitglue-mcp ./cmd/mcp
```

## Configuration

All configuration is via environment variables:

| Variable | Required | Description |
|----------|----------|-------------|
| `FITGLUE_API_URL` | no | Deployment base URL. Defaults to `https://fitglue.tech`; `http://127.0.0.1:8102` once the services run on the box (F3). |
| `FITGLUE_API_TOKEN` | yes | The `api-client` static bearer token: the value the gateway was started with. It does not expire; rotate it on both sides together. |

The token lives in Secret Manager (`gcloud secrets versions access latest
--secret fitglue-api-token`) while the services are on Cloud Run, and in
`~/.jarvis/fitglue.env` on the box.

## Register with Claude Code

```bash
claude mcp add fitglue \
  -e FITGLUE_API_TOKEN=... \
  -- /path/to/server/bin/fitglue-mcp
```

Then ask Claude things like "which of my pipelines failed this week and why?".

## Tools

All V0 tools are read-only (annotated `readOnlyHint: true`) and return the
gateway's JSON responses verbatim:

| Tool | Endpoint |
|------|----------|
| `get_profile` | `GET /users/me` |
| `list_integrations` | `GET /users/me/integrations` |
| `list_activities` | `GET /users/me/activities` |
| `get_activity` | `GET /users/me/activities/{id}` |
| `get_activity_stats` | `GET /users/me/activities/stats` |
| `list_pipelines` | `GET /users/me/pipelines` |
| `get_pipeline` | `GET /users/me/pipelines/{id}` |
| `list_pipeline_runs` | `GET /users/me/pipelines/{id}/runs` |
| `get_pipeline_run` | `GET /users/me/pipelines/{id}/runs/{runId}` |

Write tools (create/update pipelines, backfills, reposts) are deliberately
deferred until the remote server exists with scope-based OAuth to gate them.

## Testing

```bash
cd src/go && go test -short ./cmd/mcp/
```

The tests cover config validation, the gateway client, and an in-memory MCP
session exercising the registered tools. To poke at the
server interactively:

```bash
npx @modelcontextprotocol/inspector ./bin/fitglue-mcp
```
