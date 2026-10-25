# Makefile

# --- Variables ---
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOLINT=golangci-lint run
GO_SRC_DIR=src/go
TS_SRC_DIR=src/typescript

# --- Phony Targets ---
.PHONY: all clean build build-ctl build-box deploy-box status-box test lint build-go test-go lint-go clean-go build-ts test-ts lint-ts typecheck-ts clean-ts plugin-source plugin-enricher plugin-destination lint-codebase lint-shared-modules tools build-tools-go build-tools-ts prepare prepare-go prepare-ts test-integration test-e2e test-coverage preflight bootstrap-owner

all: generate clean lint build test

preflight:
	@echo "\n========== PREFLIGHT: Mirroring CI Pipeline =========="
	@echo "\n[1/7] Proto linting..."
	buf lint src/proto
	@echo "\n[2/7] Checking for service-level go.mod files..."
	@if find $(GO_SRC_DIR)/services -name "go.mod" 2>/dev/null | grep -q .; then \
		echo "❌ Found service-level go.mod files — all services must use the root module:"; \
		find $(GO_SRC_DIR)/services -name "go.mod"; \
		exit 1; \
	fi
	@echo "✅ All services use the root module."
	@echo "\n[3/7] Verifying generated code is in sync..."
	$(MAKE) generate
	@if ! git diff --quiet; then \
		echo "❌ Generated files are out of sync. Run 'make generate' and commit the changes."; \
		git diff --stat; \
		exit 1; \
	fi
	@echo "✅ Generated code is in sync."
	@echo "\n[4/7] Building..."
	$(MAKE) build
	@echo "\n[5/7] Linting..."
	$(MAKE) lint
	@echo "\n[6/7] Running tests..."
	$(MAKE) test
	@echo "\n[7/7] Checking coverage..."
	$(MAKE) test-coverage
	@echo "\n========== ✅ PREFLIGHT PASSED — safe to push =========="


setup:
	@echo "Setting up dependencies..."
	@echo "Installing Go dependencies..."
	cd $(GO_SRC_DIR) && $(GOCMD) mod download
	@echo "Installing Node dependencies for generation..."
	npm install
	@echo "Setup complete."

generate:
	@echo "Generating Protocol Buffers..."
	# Generate Go
	# Find all proto files recursively and pass them to protoc
	find src/proto -type f -name "*.proto" ! -path "src/proto/google/*" | xargs protoc \
		--go_out=$(GO_SRC_DIR)/pkg/types/pb \
		--go_opt=module=github.com/fitglue/server/src/go/pkg/types/pb \
		--go-grpc_out=$(GO_SRC_DIR)/pkg/types/pb \
		--go-grpc_opt=module=github.com/fitglue/server/src/go/pkg/types/pb \
		--experimental_allow_proto3_optional \
		--proto_path=src/proto
	# Generate TypeScript (requires ts-proto installed centrally)
	@echo "Generating TypeScript Protobufs..."
	@if [ -d "../web" ]; then \
		mkdir -p ../web/src/types/pb; \
		find src/proto -type f -name "*.proto" ! -path "src/proto/google/*" | xargs protoc \
			--plugin=./node_modules/.bin/protoc-gen-ts_proto \
			--experimental_allow_proto3_optional \
			--ts_proto_out=../web/src/types/pb --ts_proto_opt=outputEncodeMethods=false,outputJsonMethods=false,outputClientImpl=false,useOptionals=messages \
			--proto_path=src/proto; \
		echo "TypeScript protobufs updated at ../web/src/types/pb/"; \
	else \
		echo "Skipping TypeScript Protobuf generation (../web not found)"; \
	fi
	# Generate OpenAPI Clients
	@echo "Generating OpenAPI Clients..."
	@set -e; for dir in src/openapi/*; do \
		if [ -d "$$dir" ]; then \
			SERVICE=$$(basename $$dir); \
			echo "Processing $$SERVICE..."; \
			echo "  [GO] Generating client for $$SERVICE..."; \
			mkdir -p $(GO_SRC_DIR)/pkg/integrations/$$SERVICE; \
			oapi-codegen -package $$SERVICE -generate types,client \
				-o $(GO_SRC_DIR)/pkg/integrations/$$SERVICE/client.gen.go \
				$$dir/swagger.json; \
		fi \
	done
	# Generate per-gateway OpenAPI specs from gateway protos
	@echo "Generating per-gateway OpenAPI specs..."
	@mkdir -p docs/api/gateway
	@for proto in client webhook; do \
		echo "  Generating $$proto gateway spec..."; \
		cd src/proto && buf generate --template buf.gen.openapi.yaml --path gateway/$$proto.proto && cd ../..; \
		mv docs/api/openapi.yaml docs/api/gateway/$$proto.openapi.yaml; \
	done
	@echo "Per-gateway OpenAPI specs updated at docs/api/gateway/"
	# Generate Frontend API Types from per-gateway OpenAPI specs
	@echo "Generating Frontend API Types via openapi-typescript..."
	@if [ -d "../web" ]; then \
		for spec in client; do \
			echo "  Generating schema-$$spec.ts..."; \
			cd ../web && npx -y openapi-typescript ../server/docs/api/gateway/$$spec.openapi.yaml -o src/shared/api/schema-$$spec.ts; \
			cd ../server; \
		done; \
		echo "Frontend API types updated at ../web/src/shared/api/schema-client.ts"; \
	else \
		echo "Skipping web frontend api type generation (../web not found)"; \
	fi
	# Generate enum formatters (TS + Go)
	@echo "Generating enum formatters..."
	@npx ts-node scripts/generate-enum-formatters.ts


# --- Go Targets ---
build-go:
	@echo "Building Go services..."
	cd $(GO_SRC_DIR) && $(GOBUILD) -v ./...

build: build-go build-tools-go

build-tools-go:
	@echo "Building Go tools..."
	@mkdir -p bin
	@echo "  Building fit-gen tool..."
	cd $(GO_SRC_DIR) && $(GOBUILD) -o ../../bin/fit-gen ./cmd/fit-gen
	@echo "  Building fit-inspect tool..."
	cd $(GO_SRC_DIR) && $(GOBUILD) -o ../../bin/fit-inspect ./cmd/fit-inspect
	@echo "  Building fit-combine tool..."
	cd $(GO_SRC_DIR) && $(GOBUILD) -o ../../bin/fit-combine ./cmd/fit-combine
	@echo "  Building fitglue-mcp tool..."
	cd $(GO_SRC_DIR) && $(GOBUILD) -o ../../bin/fitglue-mcp ./cmd/mcp
	@echo "  Building fitgluectl tool..."
	cd $(GO_SRC_DIR) && $(GOBUILD) -o ../../bin/fitgluectl ./cmd/fitgluectl
	@echo "  Building bootstrap-owner tool..."
	cd $(GO_SRC_DIR) && $(GOBUILD) -o ../../bin/bootstrap-owner ./cmd/bootstrap-owner

build-ctl:
	@mkdir -p bin
	cd $(GO_SRC_DIR) && $(GOBUILD) -o ../../bin/fitgluectl ./cmd/fitgluectl

# --- Box deploy (rethink F3.1) ---
# The services run on the Jarvis box as systemd user units (deploy/box/), one
# static binary each in bin/, all bound to 127.0.0.1. Ports are fixed; Caddy
# (F3.3) and the units' *_SERVICE_URL settings depend on them:
#
#   8101 api-webhook   8102 api-client   8103 user        8104 pipeline
#   8105 activity      8106 destination
#
# `make deploy-box` is the whole deploy: build, reload units, restart the
# service units above that are currently active (not fitglue-caddy, which is
# F3.3's ingress and has its own lifecycle). It never *starts* a
# stopped unit — cutover from Cloud Run is `systemctl --user start fitglue.target`,
# done once in F3.4. Run it from ~/work/fitglue-server (the checkout the units'
# ExecStart points at). See docs/guides/box-deploy.md.
BOX_SERVICES := api-webhook api-client user pipeline activity destination
BOX_TOOLS := fitgluectl bootstrap-owner
BOX_GOFLAGS := -trimpath -ldflags="-s -w"

build-box:
	@mkdir -p bin
	@set -e; for s in $(BOX_SERVICES); do \
		echo "  building $$s"; \
		(cd $(GO_SRC_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(BOX_GOFLAGS) -o ../../bin/$$s ./services/$$s); \
	done
	@set -e; for t in $(BOX_TOOLS); do \
		echo "  building $$t"; \
		(cd $(GO_SRC_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(BOX_GOFLAGS) -o ../../bin/$$t ./cmd/$$t); \
	done
	@ls -l $(addprefix bin/,$(BOX_SERVICES) $(BOX_TOOLS))

deploy-box: build-box
	systemctl --user daemon-reload
	@active=""; for s in $(BOX_SERVICES); do \
		systemctl --user is-active --quiet fitglue-$$s.service && active="$$active fitglue-$$s.service"; \
	done; \
	if [ -n "$$active" ]; then \
		echo "restarting: $$active"; \
		systemctl --user restart $$active; \
	else \
		echo "no active fitglue-*.service units; nothing restarted (start with: systemctl --user start fitglue.target)"; \
	fi
	@$(MAKE) --no-print-directory status-box

status-box:
	@systemctl --user list-units --all --no-pager --plain 'fitglue.target' 'fitglue-*.service'

# --- Single-user bootstrap ---
# FitGlue has one user. There is no sign-up path any more; the owner's user
# document is created (or repaired) once with this target. Idempotent: an
# existing document keeps every field and subcollection and only has the
# identity fields merged in. Uses Application Default Credentials against
# GOOGLE_CLOUD_PROJECT (or PROJECT=...).
# Usage: make bootstrap-owner UID=<owner uid, = FITGLUE_OWNER_UID> EMAIL=<email> [DISPLAY_NAME="James"] [PROJECT=fitglue-server-prod]
bootstrap-owner:
ifndef UID
	$(error Usage: make bootstrap-owner UID=<owner uid> EMAIL=<email> [DISPLAY_NAME=...] [PROJECT=...])
endif
ifndef EMAIL
	$(error Usage: make bootstrap-owner UID=<owner uid> EMAIL=<email> [DISPLAY_NAME=...] [PROJECT=...])
endif
	cd $(GO_SRC_DIR) && $(GOCMD) run ./cmd/bootstrap-owner -uid "$(UID)" -email "$(EMAIL)" -display-name "$(DISPLAY_NAME)" -project "$(PROJECT)"

test:
	@echo "Testing Go services (Unit)..."
	cd $(GO_SRC_DIR) && $(GOTEST) -short -v ./pkg/... ./services/... ./cmd/... ./internal/...

test-integration:
	@echo "Running Integration Tests..."
	cd $(GO_SRC_DIR) && $(GOTEST) -run Integration -v ./...

test-e2e:
	@echo "Running E2E tests via godog..."
	cd $(GO_SRC_DIR)/tests/e2e && go run github.com/cucumber/godog/cmd/godog@latest run

test-coverage:
	@echo "Enforcing test coverage requirements..."
	@./scripts/check-coverage.sh

lint:
	@echo "Linting Go..."
	@echo "Checking formatting..."
	@cd $(GO_SRC_DIR) && test -z "$$(gofmt -l pkg services cmd internal)" || (echo "Go files need formatting. Run 'gofmt -w pkg services cmd internal'" && exit 1)
	@echo "Running go vet (excluding generated clients)..."
	@cd $(GO_SRC_DIR) && go vet $$(go list ./pkg/... ./services/... ./cmd/... ./internal/... | grep -v '/integrations/')
	@echo "Checking for direct sentry-go imports (use infra.Logger.Error instead)..."
	@if grep -rn '"github.com/getsentry/sentry-go"' $(GO_SRC_DIR) --include="*.go" \
		| grep -v "_test.go" \
		| grep -v "pkg/infrastructure/sentry/sentry.go" \
		| grep -v "pkg/framework/wrapper.go"; then \
		echo "❌ Direct sentry-go import found. Use logger.Error(ctx, ...) via infra.Logger — the SentryHandler forwards Error-level logs to Sentry automatically."; \
		exit 1; \
	fi
	@echo "✅ No direct sentry-go imports found."
	@echo "Checking for Protobuf JSON misuse..."
	@./scripts/lint-proto-json.sh

SERVICES := activity api-client api-webhook destination pipeline user

docker:
	@for service in $(SERVICES); do \
		echo "Building docker image for $$service..."; \
		docker build -t "fitglue-$$service" --build-arg SERVICE_NAME=$$service .; \
	done

local:
	@echo "Starting up 10 Cloud Run Emulators via Docker Compose..."
	docker-compose up --build

local-down:
	@echo "Tearing down local execution stack..."
	docker-compose down

integration:
	@echo "Running local integration test suite against live containers..."
	npm run test:local

clean:
	@echo "Cleaning Go..."
	cd $(GO_SRC_DIR) && $(GOCLEAN)

# --- Codebase Consistency Check ---

# --- Plugin Scaffolding ---
# Usage: make plugin-source name=garmin
#        make plugin-enricher name=weather
#        make plugin-destination name=runkeeper
plugin-source:
ifndef name
	$(error Usage: make plugin-source name=<name>)
endif
	./scripts/new-plugin.sh source $(name)

plugin-enricher:
ifndef name
	$(error Usage: make plugin-enricher name=<name>)
endif
	./scripts/new-plugin.sh enricher $(name)

plugin-destination:
ifndef name
	$(error Usage: make plugin-destination name=<name>)
endif
	./scripts/new-plugin.sh destination $(name)
