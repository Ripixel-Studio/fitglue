package formatters

import (
	"testing"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// --- FormatActivityType / ParseActivityType ---

func TestFormatActivityType_AllValues(t *testing.T) {
	cases := []struct {
		input    pbactivity.ActivityType
		expected string
	}{
		{pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED, "Workout"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_RUN, "Run"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_RIDE, "Ride"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_WALK, "Walk"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_SWIM, "Swim"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_HIKE, "Hike"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_YOGA, "Yoga"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT, "Crossfit"},
		{pbactivity.ActivityType_ACTIVITY_TYPE_ROWING, "Rowing"},
	}
	for _, c := range cases {
		got := FormatActivityType(c.input)
		if got != c.expected {
			t.Errorf("FormatActivityType(%v) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestParseActivityType_CommonTypes(t *testing.T) {
	cases := []struct {
		input    string
		expected pbactivity.ActivityType
	}{
		{"Run", pbactivity.ActivityType_ACTIVITY_TYPE_RUN},
		{"run", pbactivity.ActivityType_ACTIVITY_TYPE_RUN},
		{"Ride", pbactivity.ActivityType_ACTIVITY_TYPE_RIDE},
		{"Walk", pbactivity.ActivityType_ACTIVITY_TYPE_WALK},
		{"Swim", pbactivity.ActivityType_ACTIVITY_TYPE_SWIM},
		{"Hike", pbactivity.ActivityType_ACTIVITY_TYPE_HIKE},
		{"ACTIVITY_TYPE_RUN", pbactivity.ActivityType_ACTIVITY_TYPE_RUN},
	}
	for _, c := range cases {
		got := ParseActivityType(c.input)
		if got != c.expected {
			t.Errorf("ParseActivityType(%q) = %v, want %v", c.input, got, c.expected)
		}
	}
}

func TestParseActivityType_Unknown(t *testing.T) {
	got := ParseActivityType("definitely_not_a_real_activity_type_xyz")
	if got != pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
		t.Errorf("expected UNSPECIFIED for unknown type, got %v", got)
	}
}

// --- FormatMuscleGroup / ParseMuscleGroup ---

func TestFormatMuscleGroup_Roundtrip(t *testing.T) {
	groups := []pbactivity.MuscleGroup{
		pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST,
		pbactivity.MuscleGroup_MUSCLE_GROUP_SHOULDERS,
		pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS,
		pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS,
		pbactivity.MuscleGroup_MUSCLE_GROUP_QUADRICEPS,
		pbactivity.MuscleGroup_MUSCLE_GROUP_ABDOMINALS,
		pbactivity.MuscleGroup_MUSCLE_GROUP_LATS,
	}
	for _, g := range groups {
		formatted := FormatMuscleGroup(g)
		if formatted == "" {
			t.Errorf("FormatMuscleGroup(%v) returned empty string", g)
		}
		parsed := ParseMuscleGroup(formatted)
		if parsed != g {
			t.Errorf("FormatMuscleGroup(%v) = %q, ParseMuscleGroup(%q) = %v (expected %v)", g, formatted, formatted, parsed, g)
		}
	}
}

// --- FormatDestination / ParseDestination ---

func TestFormatDestination_Roundtrip(t *testing.T) {
	// Just test with UNSPECIFIED which always exists
	d := pbplugin.DestinationType_DESTINATION_UNSPECIFIED
	formatted := FormatDestination(d)
	parsed := ParseDestination(formatted)
	if parsed != d {
		t.Errorf("roundtrip failed for %v: got %v back", d, parsed)
	}
}

// --- FormatCloudEventType / ParseCloudEventType ---

func TestFormatCloudEventType_Roundtrip(t *testing.T) {
	types := []pbevents.CloudEventType{
		pbevents.CloudEventType_CLOUD_EVENT_TYPE_UNSPECIFIED,
		pbevents.CloudEventType_CLOUD_EVENT_TYPE_ACTIVITY_CREATED,
	}
	for _, ct := range types {
		formatted := FormatCloudEventType(ct)
		parsed := ParseCloudEventType(formatted)
		if parsed != ct {
			t.Errorf("roundtrip failed for CloudEventType %v: formatted %q, parsed %v", ct, formatted, parsed)
		}
	}
}

// --- FormatActivitySource / ParseActivitySource ---

func TestFormatActivitySource_Roundtrip(t *testing.T) {
	sources := []pbactivity.ActivitySource{
		pbactivity.ActivitySource_SOURCE_STRAVA,
		pbactivity.ActivitySource_SOURCE_FITBIT,
		pbactivity.ActivitySource_SOURCE_HEVY,
	}
	for _, s := range sources {
		formatted := FormatActivitySource(s)
		if formatted == "" {
			t.Errorf("FormatActivitySource returned empty for %v", s)
		}
		parsed := ParseActivitySource(formatted)
		if parsed != s {
			t.Errorf("roundtrip failed for %v", s)
		}
	}
}

// --- FormatEnricherProviderType / ParseEnricherProviderType ---

func TestFormatEnricherProviderType_Roundtrip(t *testing.T) {
	types := []pbplugin.EnricherProviderType{
		pbplugin.EnricherProviderType_ENRICHER_PROVIDER_LOGIC_GATE,
		pbplugin.EnricherProviderType_ENRICHER_PROVIDER_CALORIES_BURNED,
		pbplugin.EnricherProviderType_ENRICHER_PROVIDER_CADENCE_SUMMARY,
	}
	for _, pt := range types {
		formatted := FormatEnricherProviderType(pt)
		parsed := ParseEnricherProviderType(formatted)
		if parsed != pt {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", pt, formatted, parsed)
		}
	}
}

// --- FormatExecutionStatus / ParseExecutionStatus ---

func TestFormatExecutionStatus_Roundtrip(t *testing.T) {
	statuses := []pbpipeline.ExecutionStatus{
		pbpipeline.ExecutionStatus_STATUS_SUCCESS,
		pbpipeline.ExecutionStatus_STATUS_FAILED,
		pbpipeline.ExecutionStatus_STATUS_PENDING,
	}
	for _, s := range statuses {
		formatted := FormatExecutionStatus(s)
		parsed := ParseExecutionStatus(formatted)
		if parsed != s {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", s, formatted, parsed)
		}
	}
}

// --- FormatPluginType / ParsePluginType ---

func TestFormatPluginType_Roundtrip(t *testing.T) {
	types := []pbplugin.PluginType{
		pbplugin.PluginType_PLUGIN_TYPE_ENRICHER,
		pbplugin.PluginType_PLUGIN_TYPE_DESTINATION,
	}
	for _, pt := range types {
		formatted := FormatPluginType(pt)
		parsed := ParsePluginType(formatted)
		if parsed != pt {
			t.Errorf("roundtrip failed for %v", pt)
		}
	}
}

// --- FormatMuscleHeatmapPreset / ParseMuscleHeatmapPreset ---

func TestFormatMuscleHeatmapPreset_Roundtrip(t *testing.T) {
	presets := []pbplugin.MuscleHeatmapPreset{
		pbplugin.MuscleHeatmapPreset_MUSCLE_HEATMAP_PRESET_UNSPECIFIED,
	}
	for _, p := range presets {
		formatted := FormatMuscleHeatmapPreset(p)
		parsed := ParseMuscleHeatmapPreset(formatted)
		if parsed != p {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", p, formatted, parsed)
		}
	}
}

// --- FormatMuscleHeatmapStyle / ParseMuscleHeatmapStyle ---

func TestFormatMuscleHeatmapStyle_Roundtrip(t *testing.T) {
	styles := []pbplugin.MuscleHeatmapStyle{
		pbplugin.MuscleHeatmapStyle_MUSCLE_HEATMAP_STYLE_UNSPECIFIED,
	}
	for _, s := range styles {
		formatted := FormatMuscleHeatmapStyle(s)
		parsed := ParseMuscleHeatmapStyle(formatted)
		if parsed != s {
			t.Errorf("roundtrip failed for %v", s)
		}
	}
}

// --- FormatParkrunResultsState / ParseParkrunResultsState ---

func TestFormatParkrunResultsState_Roundtrip(t *testing.T) {
	states := []pbpipeline.ParkrunResultsState{
		pbpipeline.ParkrunResultsState_PARKRUN_RESULTS_STATE_UNSPECIFIED,
		pbpipeline.ParkrunResultsState_PARKRUN_RESULTS_STATE_PENDING,
	}
	for _, s := range states {
		formatted := FormatParkrunResultsState(s)
		parsed := ParseParkrunResultsState(formatted)
		if parsed != s {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", s, formatted, parsed)
		}
	}
}

// --- FormatIntegrationAuthType / ParseIntegrationAuthType ---

func TestFormatIntegrationAuthType_Roundtrip(t *testing.T) {
	types := []pbplugin.IntegrationAuthType{
		pbplugin.IntegrationAuthType_INTEGRATION_AUTH_TYPE_OAUTH,
	}
	for _, at := range types {
		formatted := FormatIntegrationAuthType(at)
		parsed := ParseIntegrationAuthType(formatted)
		if parsed != at {
			t.Errorf("roundtrip failed for %v", at)
		}
	}
}

// --- FormatConfigFieldType / ParseConfigFieldType ---

func TestFormatConfigFieldType_Roundtrip(t *testing.T) {
	types := []pbplugin.ConfigFieldType{
		pbplugin.ConfigFieldType_CONFIG_FIELD_TYPE_STRING,
		pbplugin.ConfigFieldType_CONFIG_FIELD_TYPE_BOOLEAN,
		pbplugin.ConfigFieldType_CONFIG_FIELD_TYPE_SELECT,
	}
	for _, ft := range types {
		formatted := FormatConfigFieldType(ft)
		parsed := ParseConfigFieldType(formatted)
		if parsed != ft {
			t.Errorf("roundtrip failed for %v", ft)
		}
	}
}

// --- FormatCloudEventSource / ParseCloudEventSource ---

func TestFormatCloudEventSource_Roundtrip(t *testing.T) {
	sources := []pbevents.CloudEventSource{
		pbevents.CloudEventSource_CLOUD_EVENT_SOURCE_UNSPECIFIED,
		pbevents.CloudEventSource_CLOUD_EVENT_SOURCE_STRAVA,
	}
	for _, s := range sources {
		formatted := FormatCloudEventSource(s)
		parsed := ParseCloudEventSource(formatted)
		if parsed != s {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", s, formatted, parsed)
		}
	}
}

// --- FormatVirtualGPSRoute / ParseVirtualGPSRoute ---

func TestFormatVirtualGPSRoute_Roundtrip(t *testing.T) {
	routes := []pbplugin.VirtualGPSRoute{
		pbplugin.VirtualGPSRoute_VIRTUAL_GPS_ROUTE_LONDON,
		pbplugin.VirtualGPSRoute_VIRTUAL_GPS_ROUTE_NYC,
	}
	for _, r := range routes {
		formatted := FormatVirtualGPSRoute(r)
		parsed := ParseVirtualGPSRoute(formatted)
		if parsed != r {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", r, formatted, parsed)
		}
	}
}

// --- FormatWorkoutSummaryFormat / ParseWorkoutSummaryFormat ---

func TestFormatWorkoutSummaryFormat_Roundtrip(t *testing.T) {
	formats := []pbplugin.WorkoutSummaryFormat{
		pbplugin.WorkoutSummaryFormat_WORKOUT_SUMMARY_FORMAT_COMPACT,
		pbplugin.WorkoutSummaryFormat_WORKOUT_SUMMARY_FORMAT_DETAILED,
		pbplugin.WorkoutSummaryFormat_WORKOUT_SUMMARY_FORMAT_VERBOSE,
	}
	for _, f := range formats {
		formatted := FormatWorkoutSummaryFormat(f)
		parsed := ParseWorkoutSummaryFormat(formatted)
		if parsed != f {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", f, formatted, parsed)
		}
	}
}

// --- FormatPipelineRunStatus / ParsePipelineRunStatus ---

func TestFormatPipelineRunStatus_Roundtrip(t *testing.T) {
	statuses := []pbpipeline.PipelineRunStatus{
		pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_RUNNING,
		pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_SYNCED,
		pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_FAILED,
	}
	for _, s := range statuses {
		formatted := FormatPipelineRunStatus(s)
		parsed := ParsePipelineRunStatus(formatted)
		if parsed != s {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", s, formatted, parsed)
		}
	}
}

// --- FormatDestinationStatus / ParseDestinationStatus ---

func TestFormatDestinationStatus_Roundtrip(t *testing.T) {
	statuses := []pbpipeline.DestinationStatus{
		pbpipeline.DestinationStatus_DESTINATION_STATUS_PENDING,
		pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
		pbpipeline.DestinationStatus_DESTINATION_STATUS_FAILED,
	}
	for _, s := range statuses {
		formatted := FormatDestinationStatus(s)
		parsed := ParseDestinationStatus(formatted)
		if parsed != s {
			t.Errorf("roundtrip failed for %v: formatted %q, parsed %v", s, formatted, parsed)
		}
	}
}
