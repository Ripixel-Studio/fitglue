# Enricher Testing Guide

This guide provides testing procedures for enricher providers in the FitGlue pipeline.

> [!IMPORTANT]
> The `./fitglue-admin` CLI referenced in setup steps below has been retired, and the admin API that replaced it has since been removed too. Set up users and pipelines through the web dashboard or direct Firestore access. The test scenarios and validation steps themselves remain accurate.

## Overview

Enrichers transform and enhance standardized activities before they are sent to destinations like Strava. Each enricher can:
- Add metadata (name, description, activity type)
- Inject data streams (heart rate, GPS coordinates)
- Generate artifacts (FIT files)

## Available Enrichers

| Provider | Purpose | Trigger Condition | Output |
|----------|---------|-------------------|--------|
| **Workout Summary** | Exercise breakdown | `StrengthSets` present | Description text |
| **Muscle Heatmap** | Muscle activation visualization | `StrengthSets` present | Description (emoji chart) |
| **Virtual GPS** | Synthetic GPS routes | `TotalDistance > 0` AND no GPS | Lat/Long streams |
| **Source Link** | Links back to source | `ExternalId` present | Description (URL) |
| **Fitbit Heart Rate** | HR data from Fitbit | Fitbit integration enabled | Heart rate stream |
| **Type Mapper** | Remaps activity types | Title keyword match | Activity type |
| **Parkrun** | Detects Parkrun events | Location/time match | Title, tags |
| **Logic Gate** | Rule-based pipeline control | Configurable rules | Continue/Halt |

---

## Test Scenario 1: Strength Training (Full Enrichment)

### Objective
Validate all description-based enrichers on a typical strength workout.

### Setup
```bash
# Create test user
./fitglue-admin users:create

# Configure Hevy
./fitglue-admin users:configure-hevy <user-id>

# Add pipeline with all enrichers
./fitglue-admin users:add-pipeline <user-id>
# Source: SOURCE_HEVY
# Enrichers: Metadata Passthrough → Workout Summary → Muscle Heatmap → Source Link
# Destinations: strava
```

### Test Data
- **Workout Type**: Strength training
- **Exercises**:
  - Bench Press: 4 sets × 8 reps @ 100kg (Chest)
  - Squats: 3 sets × 10 reps @ 120kg (Quadriceps)
  - Deadlifts: 5 sets × 5 reps @ 140kg (Lower Back)
- **Duration**: 45 minutes
- **Distance**: 0 meters

### Expected Output

**Activity Description** (concatenated):
```
Morning Strength Session

Bench Press: 4 × 8 @ 100.0kg
Squats: 3 × 10 @ 120.0kg
Deadlifts: 5 × 5 @ 140.0kg

💪 Muscle Activation:
Chest: 🟪🟪🟪🟪⬜
Quadriceps: 🟪🟪🟪⬜⬜
Lower Back: 🟪🟪🟪🟪🟪

View on Hevy: https://hevy.com/workout/abc123
```

### Validation Steps
```bash
# 1. Trigger workout (via Hevy webhook or manual)
# 2. Check enricher execution
./fitglue-admin executions:list -s enricher -u <user-id> --limit 1

# 3. Get execution details
./fitglue-admin executions:get <execution-id>

# 4. Verify output JSON
# - applied_enrichments: ["metadata-passthrough", "workout-summary", "muscle-heatmap", "source-link"]
# - Check description field contains all sections

# 5. Download FIT file
./fitglue-admin gcs:download <fit-file-uri>

# 6. Inspect FIT file
fit-inspect <file.fit>
# - Should contain NO GPS data (strength training)
# - Should contain session summary
```

### Success Criteria
- ✅ Description contains workout summary
- ✅ Description contains muscle heatmap
- ✅ Description contains source link
- ✅ FIT file generated successfully
- ✅ No GPS coordinates in FIT file
- ✅ Activity uploads to Strava with correct description

---

## Test Scenario 2: Virtual GPS (Cardio Activity)

### Objective
Validate Virtual GPS provider generates realistic GPS tracks for activities without location data.

### Setup
```bash
# Add pipeline with Virtual GPS
./fitglue-admin users:add-pipeline <user-id>
# Source: SOURCE_HEVY (or SOURCE_TEST)
# Enrichers: Virtual GPS
# Destinations: strava
```

### Test Data
Create a test activity with:
- **Type**: Running
- **Duration**: 1800 seconds (30 minutes)
- **Distance**: 5000 meters (5km)
- **GPS Data**: None (empty records)

### Expected Behavior
1. **Route Selection**: London Hyde Park (default)
2. **Distance Projection**: 5km mapped onto route
3. **Looping**: If route < 5km, loops around
4. **Timestamps**: 1800 records (1Hz) with interpolated coordinates

### Validation Steps
```bash
# 1. Publish test activity
# (Use test script or manual Pub/Sub publish)

# 2. Check enricher logs
./fitglue-admin executions:get <execution-id>
# Look for: "virtual_gps_route": "London Hyde Park (Approx)"

# 3. Download FIT file
./fitglue-admin gcs:download <fit-file-uri>

# 4. Inspect GPS data
fit-inspect <file.fit> | grep -i "position"
# Should show continuous lat/long values

# 5. Verify route
# - Plot coordinates on map (use online tool)
# - Should follow Hyde Park perimeter
# - Should loop if distance > route length
```

### Success Criteria
- ✅ FIT file contains `position_lat` and `position_long` fields
- ✅ GPS coordinates follow London Hyde Park route
- ✅ Total distance matches input (5000m)
- ✅ Timestamps are continuous (1Hz, 1800 records)
- ✅ Coordinates are realistic (within London bounds)
- ✅ Activity appears on Strava map

### Edge Cases
- **Distance = 0**: Virtual GPS should NOT apply
- **Existing GPS**: Virtual GPS should skip (unless `force: true`)
- **Very long distance**: Should loop route multiple times

---

## Test Scenario 3: Fitbit Heart Rate Integration

### Objective
Validate Fitbit HR provider fetches and merges heart rate data.

### Setup
```bash
# Connect Fitbit
./fitglue-admin users:connect <user-id> fitbit
# Visit OAuth URL to authorize

# Add pipeline with Fitbit HR
./fitglue-admin users:add-pipeline <user-id>
# Source: SOURCE_HEVY
# Enrichers: Fitbit Heart Rate
# Destinations: strava
```

### Test Data
- **Workout**: Any activity with valid timestamp
- **Fitbit Account**: Must have HR data for workout time period

### Expected Behavior
1. **API Call**: Fetches HR data from Fitbit for workout time range
2. **Interpolation**: Maps HR values to 1Hz records
3. **Merge**: HR values inserted into FIT file records

### Validation Steps
```bash
# 1. Trigger workout
# 2. Check enricher logs
./fitglue-admin executions:get <execution-id>
# Look for: "Retrieved Fitbit HR points=<N> duration=<seconds>"

# 3. Verify provider execution
# - provider_name: "fitbit-heart-rate"
# - status: "SUCCESS"
# - metadata.hr_source: "fitbit"
# - metadata.hr_points: <count>

# 4. Download FIT file
./fitglue-admin gcs:download <fit-file-uri>

# 5. Inspect HR data
fit-inspect <file.fit> | grep -i "heart"
# Should show heart_rate values (60-200 bpm range)
```

### Success Criteria
- ✅ Fitbit API called successfully
- ✅ HR data fetched for correct time range
- ✅ FIT file contains `heart_rate` field
- ✅ HR values are realistic (60-200 bpm)
- ✅ HR data visible on Strava activity

### Troubleshooting
- **"Fitbit integration disabled"**: User hasn't authorized Fitbit
- **"No HR data found"**: Fitbit account has no data for that time
- **Token expired**: Re-run `users:connect` to refresh OAuth tokens

---

## Test Scenario 4: Multiple Pipelines

### Objective
Validate that multiple pipelines can be configured for the same source.

### Setup
```bash
# Pipeline 1: Full enrichment for Strava
./fitglue-admin users:add-pipeline <user-id>
# Source: SOURCE_HEVY
# Enrichers: Metadata → Summary → Heatmap → Fitbit HR → Virtual GPS → Link
# Destinations: strava

# Pipeline 2: Minimal enrichment (test/backup)
./fitglue-admin users:add-pipeline <user-id>
# Source: SOURCE_HEVY
# Enrichers: Metadata Passthrough
# Destinations: strava
```

### Expected Behavior
- **Enricher**: Processes BOTH pipelines
- **Router**: Routes to Strava TWICE
- **Strava**: Two separate activities uploaded

### Validation Steps
```bash
# 1. Trigger one workout
# 2. Check enricher output
./fitglue-admin executions:get <execution-id>
# - published_count: 2
# - published_events: [pipeline1, pipeline2]

# 3. Check router executions
./fitglue-admin executions:list -s router -u <user-id> --limit 5
# Should see TWO router executions

# 4. Check Strava
# Should see TWO activities (one with full description, one minimal)
```

### Success Criteria
- ✅ Both pipelines execute successfully
- ✅ Two separate FIT files generated
- ✅ Two activities appear on Strava
- ✅ Descriptions differ based on enrichers

---

## Provider-Specific Configuration

### Virtual GPS
**Input Config Options**:
```json
{
  "route": "london",  // Route name (default: "london")
  "force": "true"     // Overwrite existing GPS (default: false)
}
```

**Available Routes**:
- `london`: London Hyde Park (~4km loop)
- *(Future: `nyc`, `paris`, etc.)*

### Fitbit Heart Rate
**Input Config Options**:
```json
{
  "priority": "high"  // (Optional, for future use)
}
```

**Requirements**:
- User must have Fitbit integration enabled
- OAuth tokens must be valid
- Fitbit account must have HR data for workout time

### Muscle Heatmap
**Input Config Options**:
```json
{}  // No config required
```

**Coefficients** (hardcoded):
- Quadriceps, Chest, Lats: 1.0
- Shoulders, Biceps, Triceps: 0.7
- Calves, Forearms: 0.5

### Logic Gate
**Input Config Options**:
```json
{
  "match_mode": "all",      // "all", "any", or "none"
  "rules": "[...]",          // JSON array of rule objects
  "on_match": "continue",    // "continue" or "halt"
  "on_no_match": "halt"      // "continue" or "halt"
}
```

**Rule Format**:
```json
{
  "field": "activity_type",  // Field to match
  "op": "eq",                // Operator (eq, in, gt, lt, contains, near)
  "values": ["Run"],         // Values to match against
  "negate": false            // Invert match result
}
```

**Supported Fields**:
| Field | Operators | Values Example |
|-------|-----------|----------------|
| `activity_type` | `eq` | `["Run"]` |
| `days` | `in` | `["Mon", "Wed", "Sat"]` |
| `time_start` | `gt`, `lt`, `eq` | `["09:00"]` |
| `time_end` | `gt`, `lt`, `eq` | `["17:00"]` |
| `location` | `near` | `["51.5074", "-0.1278", "500"]` |
| `title_contains` | `contains` | `["Zwift"]` |
| `description_contains` | `contains` | `["test"]` |

---

## Test Scenario 5: Type Mapper

### Objective
Validate Type Mapper correctly overrides activity type based on title keywords.

### Setup
```bash
# Add pipeline with Type Mapper
./fitglue-admin users:add-pipeline <user-id>
# Source: SOURCE_HEVY
# Enrichers: Type Mapper
# Inputs: rules=[{"substring": "Yoga", "target_type": "YOGA"}]
# Destinations: strava
```

### Test Data
- **Activity 1**: "Sunday Morning Yoga" (Type: Weight Training) -> Expect YOGA
- **Activity 2**: "Heavy Deadlifts" (Type: Weight Training) -> Expect WEIGHT_TRAINING (no match)

### Validation Steps
```bash
# 1. Trigger Activity 1
# 2. Check enricher logs
./fitglue-admin executions:get <execution-id>
# Look for: "rule_matched": "true", "new_type": "YOGA"

# 3. Check Strava
# Activity type should be "Yoga" on Strava
```

### Success Criteria
- ✅ "Sunday Morning Yoga" appears as "Yoga"
- ✅ "Heavy Deadlifts" remains "Weight Training"

---

## Test Scenario 6: Workout Summary Stats

### Objective
Validate Workout Summary includes headline statistics when configured.

### Setup
```bash
# Add pipeline with Workout Summary (Stats Enabled)
./fitglue-admin users:add-pipeline <user-id>
# Enricher: Workout Summary
# Inputs: {"format": "detailed", "show_stats": "true"}
```

### Expected Output
```
Workout Summary:
📊 4 sets • 3,200kg volume • 32 reps • Heaviest: 100kg (Bench Press)

- Bench Press: 4 x 8 × 100.0kg
```

### Validation Steps
- Visual check of description on Strava or in execution logs.

---

## Automated Testing

### Unit Tests
```bash
# Go enricher provider tests
cd src/go
go test -v ./internal/pipeline/...

# With coverage
go test -coverprofile=coverage.out ./internal/pipeline/...
go tool cover -html=coverage.out
```

### Integration Tests
```bash
# Against local stack
make test-e2e

# Against dev environment
make test-integration
```

- [Testing Guide](../development/testing.md) - Full test strategy and patterns

---

## Common Issues

### "Provider not found, skipping"
**Cause**: Provider not registered or `init()` side-effect import missing
**Solution**: Verify provider is imported in the service's `main.go` (blank import `_ "path/to/provider"`)

### "No GPS data generated"
**Cause**: Activity has `TotalDistance = 0` or existing GPS
**Solution**: Check activity data, use `force: true` config

### "Muscle heatmap empty"
**Cause**: No `StrengthSets` in activity
**Solution**: Ensure activity contains strength training data

### "Fitbit HR failed"
**Cause**: OAuth tokens expired or invalid
**Solution**: Re-authorize via `users:connect <user-id> fitbit`

### "Description not concatenated"
**Cause**: Orchestrator merge logic issue
**Solution**: Check enricher order, verify description append logic

---

## Best Practices

1. **Test in Order**: Start with simple enrichers (Metadata) before complex ones (Virtual GPS)
2. **Verify Logs**: Always check execution logs for provider-specific messages
3. **Inspect FIT Files**: Use `fit-inspect` to validate binary output
4. **Check Strava**: Final validation should be visual inspection on Strava
5. **Clean Up**: Remove test pipelines and users after testing

---

## Next Steps

- Add more routes to Virtual GPS library
- Implement PR Tracker enricher
- Create automated E2E tests for all enrichers
