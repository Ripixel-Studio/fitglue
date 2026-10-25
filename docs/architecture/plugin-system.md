# Plugin System Architecture

FitGlue uses a **type-safe, self-registering plugin architecture** for extensible data processing. All plugins are implemented in Go.

## Plugin Types

| Type | Language | Purpose | Examples |
|------|----------|---------|----------|
| **Source** | Go | Ingests data via webhooks from external services | Hevy, Fitbit, Strava, Intervals.icu, GitHub, Parkrun |
| **Enricher** | Go | Transforms/enhances activities in pipeline | HR Summary, Weather, Parkrun, AI Companion |
| **Destination** | Go | Uploads processed activities to external services | Strava, TrainingPeaks, Intervals.icu, Hevy, Fitbit, Google Sheets, GitHub, Showcase |

## Available Plugins

### Sources (Data Ingestion)
| Source | Auth Type | Features |
|--------|-----------|----------|
| Hevy | API Key / HMAC | Strength workouts, webhook sync |
| Fitbit | OAuth | Activity notifications, polling |
| Strava | OAuth | Activity webhooks |
| FIT Upload | Static bearer token | Manual FIT file upload |
| Parkrun Results | Public ID | Race results via athlete ID |

### Enrichers (Pipeline Steps)

| Category | Enrichers |
|----------|-----------|
| **Data** | Fitbit HR, FIT File HR, Spotify Tracks, Weather, Running Dynamics |
| **Stats** | Heart Rate Summary, Pace Summary, Speed Summary, Power Summary, Cadence Summary, Elevation Summary, Training Load (TRIMP), Personal Records |
| **Visual** | Muscle Heatmap, Muscle Heatmap Image, Route Thumbnail |
| **Detection** | Parkrun Detector, Location Naming, Condition Matcher |
| **Transform** | Type Mapper, Auto Increment, Logic Gate, Activity Filter |
| **Input** | User Input, Hybrid Race Tagger |
| **AI** | AI Companion, AI Banner |

### Destinations (Data Export)
| Destination | Auth Type | Features |
|-------------|-----------|----------|
| Strava | OAuth | FIT upload, title/description sync |
| TrainingPeaks | OAuth | Workout upload |
| Intervals.icu | API Key | Activity upload |
| Hevy | API Key | Sync back to Hevy |
| Fitbit | OAuth | Activity upload to Fitbit |
| Google Sheets | OAuth | Activity data export |
| GitHub | OAuth | Activity data export |
| Showcase | Built-in | Public activity sharing |

## Registration Patterns

### Enrichers (Self-Registration via `init()`)

Enrichers live in `internal/pipeline/` and register themselves:

```go
// internal/pipeline/providers/weather.go
func init() {
    plugin.RegisterManifest(pb.EnricherProviderType_ENRICHER_PROVIDER_WEATHER, &pb.PluginManifest{
        Id:          "weather",
        Name:        "Weather",
        Description: "Adds weather conditions based on activity location and time",
        Icon:        "🌤️",
        Tier:        pb.UserTier_USER_TIER_PRO,
        ConfigSchema: []*pb.ConfigFieldSchema{
            {Key: "units", Label: "Units", FieldType: pb.ConfigFieldType_CONFIG_FIELD_TYPE_SELECT},
        },
    })
    Register(NewWeatherProvider())
}
```

### Sources (Self-Registration via `init()`)

Sources live in `services/api-webhook/internal/webhook/sources/{name}/` and register via the `SourceRegistry`:

```go
// services/api-webhook/internal/webhook/sources/strava/provider.go
func init() {
    sources.Register(&StravaProvider{})
}

type StravaProvider struct{}

func (p *StravaProvider) Source() string { return "strava" }
func (p *StravaProvider) VerifyWebhook(r *http.Request) error { ... }
func (p *StravaProvider) ResolveUser(ctx context.Context, body []byte) (string, error) { ... }
func (p *StravaProvider) FetchActivity(ctx context.Context, externalID string, creds *pb.OAuthTokens) (*pb.StandardizedActivity, error) { ... }
```

### Destinations (Self-Registration via `init()`)

Destinations live in `services/destination/internal/destination/uploaders/{name}/` and register via the `DestinationRegistry`.

## Scaffolding New Plugins

```bash
# Add a new data source (Go webhook provider)
make plugin-source name=suunto

# Add a new enricher (Go pipeline step)
make plugin-enricher name=sleep_score

# Add a new destination (Go uploader)
make plugin-destination name=runkeeper
```

### What Gets Generated

#### Source (`make plugin-source name=NAME`)

| Generated | Location |
|-----------|----------|
| Provider file | `services/api-webhook/internal/webhook/sources/{name}/provider.go` |
| Proto enum | Auto-added to `activity.proto` |
| Type regeneration | Runs `make generate` automatically |

**Remaining manual steps:**
1. Implement `VerifyWebhook`, `ResolveUser`, `FetchActivity`
2. Add API client for the source if needed
3. Update documentation

#### Enricher (`make plugin-enricher name=NAME`)

| Generated | Location |
|-----------|----------|
| Provider file | `internal/pipeline/providers/{name}.go` |
| Proto enum | Auto-added to `pipeline.proto` |
| Type regeneration | Runs `make generate` automatically |

**Remaining manual steps:**
1. Implement the `Enrich()` method
2. Add config fields to the manifest

#### Destination (`make plugin-destination name=NAME`)

| Generated | Location |
|-----------|----------|
| Uploader file | `services/destination/internal/destination/uploaders/{name}/uploader.go` |
| Proto enum | Auto-added to `events.proto` |
| Type regeneration | Runs `make generate` automatically |

**Remaining manual steps:**
1. Implement upload logic
2. Register OAuth integration if needed

## Tier Restrictions

Some enrichers are restricted to higher tiers:

| Tier | Available Enrichers |
|------|---------------------|
| Free | Heart Rate Summary, Elevation, Type Mapper, User Input |
| Pro | All Free + Weather, Location, Training Load, Personal Records |
| Athlete | All Pro + AI Companion, AI Banner, Spotify Tracks |

Tier enforcement is done via `pb.PluginManifest.Tier` and checked during enrichment by `service.pipeline`.

## Configuration Schema

Plugins define their configuration using `ConfigFieldSchema`:

| Field Type | Description | Example |
|------------|-------------|---------|
| `STRING` | Text input | API keys, names |
| `NUMBER` | Numeric input | Timeout values |
| `BOOLEAN` | Toggle | Enable/disable features |
| `SELECT` | Dropdown | Format options |
| `MULTI_SELECT` | Multi-choice | Days of week |
| `KEY_VALUE_MAP` | Key-value pairs | Type mappings |
| `ACTIVITY_TYPE_SELECT` | Activity type picker | Filter by type |

## Related Documentation

- [Architecture Overview](overview.md) - System components
- [API Layers](api-layers.md) - Webhook processing and SourceProvider interface
- [Go Services](go-services.md) - Service directory structure
