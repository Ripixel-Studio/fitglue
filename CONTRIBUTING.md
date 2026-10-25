# Contributing to FitGlue

Thank you for your interest in contributing to FitGlue! This document provides guidelines for contributing to the project.

## Code of Conduct

Be respectful and constructive. We're all here to build something great.

## Getting Started

### Prerequisites

- Go 1.25+
- Node.js 20+ (Required for Protobuf and OpenAPI frontend type generation)
- `protoc` (Protocol Buffers compiler)
- Google Cloud SDK (for deployment)

### Setup

```bash
# Clone the repo
git clone https://github.com/ripixel/fitglue-server.git
cd fitglue-server

# Install dependencies
make setup

# Build everything
make build

# Run tests
make test
```

## Development Workflow

### Adding New Features

1. **New RPC Endpoint**: Define in `src/proto`, run `make generate`, and implement the interface in the respective `internal` package and `service`.
2. **New Webhook Source**: Implement the `SourceProvider` interface in `internal/webhook` and register it inside `services/api-webhook/main.go`.
3. **Deploying**: merging to `main` still applies terraform and rolls the Cloud Run services from CircleCI until F4; after F3 it is `make deploy` on the box. See `docs/infrastructure/cicd.md`.

2. **Proto changes?** Regenerate types:
   ```bash
   make generate
   ```

3. **Run tests before committing:**
   ```bash
   make test
   make lint
   ```

### Code Style

- **Go**: Follow standard Go conventions, use `gofmt`
- **TypeScript**: ESLint rules in `.eslintrc` (utilized strictly in frontend and tooling scripts)
- **Commits**: Use conventional commits (feat, fix, docs, refactor, test, chore)

### Pull Request Guidelines

1. Fork the repository
2. Create a feature branch: `git checkout -b feat/my-feature`
3. Make your changes with tests
4. Run `make test && make lint`
5. Commit with a descriptive message
6. Push and open a PR

### What CI checks

GitHub Actions runs `make build`, `make lint` and `make test` on every PR. Nothing else runs on a PR.

### PR Checklist

- [ ] Tests pass (`make test`)
- [ ] Lint passes (`make lint`)
- [ ] Build succeeds (`make build`)
- [ ] Documentation updated if needed
- [ ] Proto types regenerated if proto files changed

## Project Structure

```text
server/
├── src/go/               # Go monorepo
│   ├── services/         # Cloud Run Entrypoints
│   ├── internal/         # Business Logic
│   └── pkg/              # Shared libraries
├── src/proto/            # Protocol Buffer definitions
├── terraform/            # Infrastructure as Code
└── scripts/              # Development scripts
```

## Troubleshooting

If something isn't working, start with the [Troubleshooting Guide](docs/guides/troubleshooting.md) — it maps common failure types to the exact service, code path, and log filter you need.

## Questions?

Open an issue for questions, bugs, or feature requests.
