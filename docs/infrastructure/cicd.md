# CI/CD

FitGlue is a single-user system being moved onto the box
(`~/jarvis-memory/specs/fitglue/personal-fitglue.md`). CI is therefore small and
shrinking: pull requests are checked on GitHub Actions; the cloud deploy on
`main` is a holdover that goes in F4.

## Pull requests — GitHub Actions

`.github/workflows/ci.yml` is the only PR check. On every PR targeting `main` it
runs, with a Go module/build cache keyed off `src/go/go.sum`:

1. `make build` — compile every service and CLI tool
2. `make lint` — `gofmt`, `go vet`, the sentry-import check, the proto-JSON check
3. `make test` — unit tests (`go test -short`)

That is all a PR needs. Proto linting, code generation and drift checks run
locally through `make preflight` (the pre-push hook in `.githooks/`). There is
no preview deploy, no release note generation and no coverage gate in CI.

## `main` — CircleCI deploy (RETIRED IN F4)

`.circleci/config.yml` is filtered to `main` and runs one chain:

1. `build-and-test` — the same `make build && make lint && make test`
2. `deploy-prod` — authenticate to GCP with CircleCI OIDC
   (`circleci-pool` / `circleci-deployer@fitglue-server-prod`), build and push a
   Docker image per service to Artifact Registry (content-hash cached), then
   `terraform apply -auto-approve -var-file=envs/prod.tfvars -var image_tag=<sha>`

It is still here for one reason: the F1.x removal PRs delete cloud resources
from terraform, and this apply-on-merge is what actually destroys them. It is
also, for now, how a new build reaches Cloud Run.

What replaces it, and when:

| Today (CircleCI) | Replacement | Task |
|---|---|---|
| Docker build + push, Cloud Run revision | `make deploy-box` on the box (static binaries + systemd user units, `docs/guides/box-deploy.md`) | F3.1 |
| OIDC → `circleci-deployer` SA | `fitglue-box` service-account key on the box | F3.2 |
| Secrets via terraform / Secret Manager | `~/.jarvis/fitglue.env` | F3.5 |
| `terraform apply` on merge | `terraform apply` run by hand from the box | F4 |

When F4 lands: delete `.circleci/config.yml`, `scripts/setup_oidc.sh`, the
CircleCI project and the workload-identity pool. Nothing else depends on them.

### OIDC in one paragraph

`scripts/setup_oidc.sh <env>` created a Workload Identity Pool
(`circleci-pool`) with a provider trusting `https://oidc.circleci.com/org/<org>`
and a `circleci-deployer` service account that pool members may impersonate.
The deploy job writes `CIRCLE_OIDC_TOKEN` to a file, builds a credential config
with `gcloud iam workload-identity-pools create-cred-config`, and exports it as
`GOOGLE_APPLICATION_CREDENTIALS` for both `gcloud` and terraform. If the deploy
fails with "The audience in ID Token does not match the expected audience", the
provider's allowed audience must be the CircleCI organisation id, not a GCP
resource path.

## Removed in F1.8

- The `create-release` job: `standard-version` tagging and changelog
  generation, the `version.json` written to `gs://fitglue-server-prod-version-config`,
  and the cross-repo trigger of a `fitglue-web` rebuild. There is no public
  changelog or store-facing version any more; `CHANGELOG.md` is hand-written.
- CircleCI `build-and-test` on pull requests (it duplicated the Actions check).
- `scripts/upload_sourcemaps.sh` (TypeScript source maps for a Go-only server).
