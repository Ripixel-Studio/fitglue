# Architecture & Workflow Decisions

## 001 - Hybrid Local Development Strategy (2025-12-19)

### Context
We needed a local development environment that is fast, isolated, but realistic.
Full local emulation (Firestore Emulator, Pub/Sub Emulator) requires heavy dependencies (Java JDK) not currently present.
Creating separate GCP projects for every developer is heavyweight.

### Decision
We adopted a **Hybrid Cloud/Local** workflow:

1.  **Run Local:** Services run locally via `make local` (Go binaries).
2.  **Real Database (Dev):** We use a shared "Dev" GCP Project (`fitglue-server`) for Firestore.
    - *Mitigation:* Developers use unique inputs/IDs during manual testing to collision.
    - *Test Isolation:* Integration tests generate random UUIDs (`user_test_<uuid>`) and clean up after themselves.
3.  **Mocked Pub/Sub:** We use a `LogPublisher` (No-Op) adapter by default.
    - *Why:* Prevents "Topic Pollution" where one dev's test event triggers another dev's local subscriber.
4.  **Future:** Formal "Test" and "Prod" environments will be separate GCP Projects managed by Terraform.

### Consequences
- **Pros:** Zero setup cost (no Java/Emulators). Wiring verification is fast. No topic collisions.
- **Cons:** Requires internet. "Dev" database is shared (but handled via unique IDs).

## 002 - Infrastructure & Environment Isolation (2025-12-19)

### Context
We need to support "Dev", "Test", and "Prod" environments.
We initially considered using the existing `fitglue-server` for Dev, but decided to start fresh to ensure consistency across all three envs.

### Decision
We will use **Terraform Workspaces** coupled with **tfvars** files.
We will create **3 Fresh GCP Projects** (leaving `fitglue-server` dormant).

1.  **Project Mapping:**
    - Workspace `dev` -> `fitglue-server-dev`
    - Workspace `test` -> `fitglue-server-test`
    - Workspace `prod` -> `fitglue-server-prod`
2.  **Configuration:**
    - `envs/dev.tfvars`, `envs/test.tfvars`, `envs/prod.tfvars`
3.  **State Management:**
    - Local State with Workspaces (`terraform.tfstate.d/`).

## 003 - Naming Convention (2025-12-19)

### Standard
`[project]-[purpose]-[environment]`

### Examples
- `fitglue-server-dev` (Backend Functions - Dev)
- `fitglue-server-prod` (Backend Functions - Prod)
- (Future) `fitglue-web-prod` (Frontend - Prod)

### Rationale
Allows grouping by project (`fitglue`), purpose (`server`/`app`), and environment (`dev`/`prod`) systematically.

## 004 - Monorepo Structure (2025-12-21)

### Context
We initially used a polyrepo-style structure with multiple `go.mod` files and specialized `make` targets to inject shared code into each function's directory. This caused:
-   Persistent `go.sum` checksum mismatches in CI.
-   Complexity in local development (workspace vs replacement directives).
-   "Missing module" errors because of tangled dependency graphs.

### Decision
We refactored the repository into a **Monorepo** structure:
-   `src/go`: Single Go module (`github.com/ripixel/fitglue-server/src/go`) containing all backend functions and shared packages.
-   `src/typescript`: TypeScript functions using npm workspaces.
-   `src/proto`: Unified Protocol Buffers.

### Consequences
-   **Pros:** Simplified build (standard Go tooling works). No code injection/sed hacking. Single source of truth for dependencies.
-   **Cons:** Deployments upload the entire module context (negligible size impact).

## 005 - User Identity & Integration Mapping (2025-12-22)

### Context
We need to map internal FitGlue users to multiple external identities (Strava ID, Hevy ID, etc.) and handle authentication securely across functions. Using simple ID references scattered across Firestore documents makes it hard to manage permissions or perform lookups (e.g., "Find the FitGlue user for Strava ID 12345").

### Decision
We introduced a **User Record Pattern** with two core entity types stored in Firestore:

1.  **User Record (`users/{userId}`)**: The single source of truth for a user's identity.
    -   Contains a map of `integrations` (e.g., `strava: "12345"`, `hevy: "abc-def"`).
    -   Contains API keys and roles.
2.  **Identity Maps (`integrations/{platform}/ids/{externalId}`)**: Reverse-lookup documents.
    -   Example: `integrations/strava/ids/998877` -> `{ userId: "uuid-5566" }`.
    -   Allows fast lookup from incoming webhooks (which only have the provider's ID) to our internal User ID.

### Consequences
-   **Pros**: O(1) lookup for webhooks. Centralized integration status.
-   **Cons**: Requires dual-write transaction when linking an account (update User + create Identity Map).

## 006 - DNS & Domain Architecture (2025-12-24)

### Context
We have three environments (Dev, Test, Prod) but one purchased domain (`fitglue.tech`). We also anticipate a future where the frontend hosting (website/app) might live in a separate Google Cloud Project from the backend server functions.

### Decision
We adopted a **Hub-and-Spoke DNS Strategy** centered on the Production environment.

1.  **DNS Hub (Prod)**:
    -   `fitglue-server-prod` holds the **Cloud DNS Managed Zone** for `fitglue.tech` (the root).
    -   This zone is the "Source of Truth" for the domain.

2.  **Environment Isolation (Subdomains)**:
    -   **Dev**: Manages its own zone `dev.fitglue.tech`. Prod delegates to it via `NS` records.
    -   **Test**: Manages its own zone `test.fitglue.tech`. Prod delegates to it via `NS` records.
    -   **Benefit**: You can tear down/rebuild the Dev environment entirely without affecting Prod.

3.  **Future Frontend Integration**:
    -   **Root (`fitglue.tech`)**: When we add a frontend (e.g., Firebase Hosting), we will likely keep the frontend code in a separate project. We will simply add the `A` and `TXT` records provided by that hosting service directly into the `fitglue-server-prod` Terraform zone.
    -   **Subdomains (`app.fitglue.tech`)**: If we use a subdomain for the app, we can use the same NS delegation strategy used for Dev/Test.

### Consequences
-   **Pros**: Clean separation of environments. Infrastructure-as-code controls the domain. Ready for multi-project architecture.
-   **Cons**: Initial manual setup step required to paste "Dev" nameservers into "Prod" zone (Chain of Trust).

## 007 - Unified URL Strategy (2025-12-24)

### Context
We need to expose various resources (Marketing Site, Dashboard, Webhooks, OAuth Callbacks) to the internet.
We considered using subdomains (e.g., `hooks.fitglue.tech`, `api.fitglue.tech`) vs. a unified domain structure.
We are prioritizing user trust, brand cohesiveness, and ease of cross-origin (CORS) management.

### Decision
We will use a **Unified Domain Strategy** served via **Firebase Hosting Rewrites**.

1.  **Single Origin**: All public endpoints will be accessible via the root domain (per environment).
    -   Prod: `https://fitglue.tech`
    -   Dev: `https://dev.fitglue.tech`
2.  **Path-Based Routing**:
    -   **Frontend**: `/` and `/app/**` -> Served by Firebase Hosting (Static/SPA).
    -   **Webhooks**: `/hooks/{provider}` -> Rewrites to Cloud Function `webhook-handler`.
    -   **API**: `/api/{version}/**` -> Rewrites to Cloud Function `api-service`.
    -   **Auth**: `/auth/{provider}/callback` -> Rewrites to Auth Handler.

### Consequences
-   **Pros**:
    -   Eliminates CORS issues for the dashboard calling the API.
    -   Simplified SSL identity.
    -   Clean, professional URLs (`fitglue.tech/app` vs `app.fitglue.tech`).
    -   Abstracts the underlying backend (Cloud Functions) from the public interface.
-   **Cons**:
    -   Requires Firebase Hosting configuration (`firebase.json`) to be the entry point.
    -   Slight latency overhead from the Hosting proxy (typically negligible).

### Update to 007 (2025-12-24)
**Strict Separation of Concerns**: We validated that the Firebase Hosting / Routing layer should **NOT** be part of the `fitglue-server` infrastructure. It will live in a distinct `fitglue-web` project. The server project will only provide the raw Cloud Functions and the DNS Zone, to which the Web project will attach its records.

## 008 - Project & Repository Architecture (2025-12-24)

### Context
We require:
1.  **Unified URLs**: `fitglue.tech/hooks/...` should mask backend functions.
2.  **Strict Code Separation**: Frontend code and Backend code must live in separate Git repositories.
3.  **Environment Isolation**: Dev, Test, Prod must be completely separate.

### Constraint
**Firebase Hosting Rewrites** (the technology that powers component 1) only work if the Hosting site and the Cloud Functions reside in the **same Google Cloud Project**.

### Decision
We will adopt a **Shared Project / Separate Repository** model.

1.  **GCP Projects**:
    One project per environment acting as the "Environment Container".
    -   `fitglue-dev`
    -   `fitglue-test`
    -   `fitglue-prod`
    (Note: Previously named `fitglue-server-*`, we are likely renaming to reflect their broader scope).

2.  **Repositories**:
    -   **Repo A (`fitglue-server`)**: Deploys Cloud Functions, Firestore, Pub/Sub to the project.
    -   **Repo B (`fitglue-web`)**: Deploys Static Files and Firebase Hosting Configuration to the **same** project.

### Consequences
-   **Pros**:
    -   Enables zero-cost routing/proxying via Firebase Hosting.
    -   Keeps codebases clean and decoupled.
    -   Simplifies IAM (Service Account A deploys Server, Service Account B deploys Web).
-   **Cons**:
    -   Coordination required: Repo B cannot "rewrite" to a function that Repo A hasn't deployed yet.

## 009 - Connector Stateless Architecture (2026-01-02)

### Context
Connectors were originally designed as singletons or reused instances where `setContext(ctx)` was called before processing. This introduced a critical race condition risk where concurrent requests could overwrite the context of a running connector, causing data leakage or authorization errors between users.

### Decision
We moved to a **Per-Request Instantiation** model for Connectors.

1.  **Framework Change**: `createWebhookProcessor` now accepts a `ConnectorConstructor` (Class) instead of an instance.
2.  **Lifecycle**: A new Connector instance is created for *every* incoming request.
3.  **Context Injection**: `FrameworkContext` is passed strictly via the **constructor**. `setContext` has been removed.
4.  **Immutability**: The `context` property on `BaseConnector` is `protected readonly`.

### Consequences
-   **Pros**: Thread-safe by design. Impossible to process a request with the wrong user context. Cleaner testing (no mock hoisting/resetting issues).
-   **Cons**: Slight memory overhead for object creation per request (negligible in JSV8).

## 010 - Strict Data Access Typing (2026-01-02)

### Context
Our Firestore interaction layer (`UserStore`, `ActivityStore`) utilized generic `update(id, data: any)` methods. As the data model complexity grew (nested integrations, pipelines), usage of "dot-notation" keys (e.g., `'integrations.hevy'`) led to widespread use of `any` in Service layers, bypassing TypeScript validation and risking runtime errors from schema mismatches.

### Decision
We enforced **Strict Type Safety** in the Data Access Layer.

1.  **Partial<T> Enforcement**: The generic `update` method now strictly requires `Partial<RecordType>`, disallowing ad-hoc or nested keys.
2.  **Specific Methods**: Complex updates are encapsulated in specific, strongly-typed methods on the Store class (e.g., `UserStore.setIntegration`, `UserStore.addPipeline`).
3.  **Service Parity**: Services must delegate to these typed methods rather than constructing unrestricted update objects.

### Consequences
-   **Pros**: Compile-time validation of all DB writes. Refactoring safety. Self-documenting allowed operations.
-   **Cons**: More boilerplate code in Store classes for every specific update pattern.

## 011 - Service vs Store Responsibility (2026-01-02)

### Context
As the application scales, the boundary between business logic and data access must be improved. We observed Services explicitly constructing complex database-specific structures (like Firestore dot-notation keys) and Stores occasionally leaking implementation details or lacking strict enforcement.

### Decision
We formally separated responsibilities between **Services** and **Stores**:

1.  **Stores (Data Access Layer)**:
    *   **Sole Responsibility**: Type-safe CRUD operations and database-specific logic.
    *   **Scope**: encapsulating Firestore implementations (e.g., `FieldValue.arrayUnion`, dot-notation paths).
    *   **Rule**: Must not contain business logic (e.g., "Can this user perform this action?").
    *   **Rule**: Must enforce strict input types (e.g., `Partial<T>` or specific arguments).

2.  **Services (Domain Layer)**:
    *   **Sole Responsibility**: Orchestrating business workflows and enforcing business rules.
    *   **Scope**: Validating inputs, calling external APIs, and coordinating multiple Store operations.
    *   **Rule**: Must **never** access the database driver directly (e.g., `admin.firestore()`).
    *   **Rule**: Must delegate all persistence to proper Store methods.

### Consequences
*   **Pros**: Decouples business logic from database technology. Improves testability by allowing Services to run with mocked Stores.
*   **Cons**: Requires a strict "pass-through" method in Stores for every distinct update operation required by Services.

## 012 - Self-Describing Plugin Registry v4.3 (2026-01-15)

### Context
Plugin configuration was documented in a static markdown file (`ENRICHER_CONFIG.md`). This led to:
-   Documentation drift as new plugins were added without updating the doc.
-   No programmatic access to configuration schemas for the frontend.
-   Duplicate maintenance burden (code + docs).

### Decision
We implemented a **Self-Describing Plugin Registry**:

1.  **Single Source of Truth**: Plugin manifests are defined in code (`registry.ts` for TypeScript, `init()` functions for Go enrichers).
2.  **API Endpoint**: `GET /api/registry` returns all plugin manifests with full configuration schemas.
3.  **Rich Manifests**: Each plugin includes `configSchema`, `marketingDescription`, `features`, and `transformations` for UI rendering.
4.  **Obsolete Docs Deleted**: `ENRICHER_CONFIG.md` was removed as it's now redundant.

### Consequences
-   **Pros**: Zero documentation drift. Frontend dynamically generates config forms. Single place to update plugin metadata.
-   **Cons**: Slightly larger registry.ts file. Requires discipline to add marketing content.

## 013 - Unified Authorization Layer (2026-01-15)

### Context
Authorization checks were scattered across handlers with inconsistent patterns. Some checked ownership inline, others delegated to Services, and the logic for "admin bypass" varied.

### Decision
We introduced a centralized **AuthorizationService**:

1.  **requireAccess(userId, resourceOwnerId)**: Checks if user can access a resource (owner match OR admin).
2.  **requireAdmin()**: Checks if user has admin privileges.
3.  **ForbiddenError**: Standard error thrown on authorization failure.
4.  **Service Location**: Part of `ctx.services.authorization` in the FrameworkContext.

### Consequences
-   **Pros**: Consistent authorization across all handlers. Single point for audit. Easy to extend with roles/permissions.
-   **Cons**: Handlers must explicitly call authorization (not automatic).

## 014 - Codebase Consistency Linter (2026-01-16)

### Context
As the codebase grew, inconsistencies emerged:
-   Proto enum values without corresponding registry entries.
-   Cloud Functions missing Terraform definitions.
-   Store methods using `any` types.
-   Services directly accessing `admin.firestore()`.

### Decision
We implemented `scripts/lint-codebase.ts` with automated checks:

1.  **Enricher Registration**: All proto enum values have registry entries.
2.  **Function Exports**: All Cloud Functions export correctly.
3.  **Terraform Coverage**: All functions have Terraform definitions.
4.  **Proto Sync**: TypeScript and Go types match proto definitions.
5.  **Store Types**: No `any` types in Store methods.
6.  **Service Boundaries**: Services don't access database drivers directly.

Integrated into CI/CD as `make lint-codebase`.

### Consequences
-   **Pros**: Catches consistency issues before deployment. Enforces architectural boundaries automatically.
-   **Cons**: Additional CI step (~10s). False positives require explicit ignores.

## 015 - Go Architecture Migration Complete (2026-02)

### Context
ADR 014 and prior decisions were made in the context of a hybrid TypeScript/Go Cloud Functions architecture (48 individually deployed functions). The `architecture-overhaul-proposal.md` document (now at `decisions/history/go-migration-proposal.md`) proposed a complete migration to 10 domain-owning Go Cloud Run services.

### Decision
The migration was approved and fully implemented. The FitGlue server backend now consists exclusively of Go services:

| # | Service | Type |
|---|---|---|
| 1 | `service.api.client` | Authenticated user HTTP gateway |
| 2 | `service.api.admin` | Admin HTTP gateway |
| 3 | `service.api.public` | Unauthenticated HTTP gateway |
| 4 | `service.api.webhook` | Inbound webhook processor |
| 5 | `service.user` | User profiles, integrations, OAuth tokens |
| 7 | `service.pipeline` | Pipeline CRUD, enrichment, routing |
| 8 | `service.activity` | Activities, showcases, exports |
| 9 | `service.registry` | Plugin manifests, categories, icons |
| 10 | `service.destination` | All destination uploaders |

All TypeScript server code (`src/typescript/`) has been removed. Inter-service communication uses gRPC with protobuf-defined contracts. The API layer is a thin HTTP marshalling layer; domain services own their Firestore collections.

### Consequences
-   **Pros**: Single language (Go), 79% reduction in deployable units, ~93% faster cold starts, compile-time type checking across service boundaries, single `go.mod`.
-   **Cons**: Loss of TypeScript test suites (replaced by Go table-driven tests), Go expertise requirement for all server contributions.

### Reference
Full proposal and rationale: [decisions/history/go-migration-proposal.md](history/go-migration-proposal.md)

