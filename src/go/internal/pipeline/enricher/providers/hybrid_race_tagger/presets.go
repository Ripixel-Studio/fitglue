package hybrid_race_tagger

// StationType defines whether a station is cardio (lap-based) or strength (set-based)
type StationType int

const (
	StationTypeRun      StationType = iota // Keep as Lap
	StationTypeCardio                      // Keep as Lap with exercise_name
	StationTypeStrength                    // Convert to StrengthSet
)

// Station defines a single station in a hybrid race
type Station struct {
	Name           string      // Exercise name for Hevy mapping
	Type           StationType // How to represent in StandardizedActivity
	DistanceMeters float64     // Expected distance (for cardio/distance-based strength)
	Reps           int32       // Expected reps (for rep-based strength like Wall Balls)
	WeightKg       float64     // Weight in kg (varies by gender/category)
	Icon           string      // Emoji icon for display
}

// RacePreset defines a complete hybrid race format
type RacePreset struct {
	ID          string    // Unique identifier
	Name        string    // Display name
	Description string    // Brief description
	RaceType    string    // e.g., "hyrox", "athx" - used for PR keys
	Stations    []Station // Ordered list of stations
}

// Hyrox weights by category and division
// Reference: the official HYROX rulebook / weight chart (hyrox.com, "The Fitness Race"),
// 25/26 season. Sled weights are "incl. sled" values. Distances and reps are the same in
// every division: SkiErg 1000m, Sled Push 50m, Sled Pull 50m, Burpee Broad Jumps 80m,
// Row 1000m, Farmers Carry 200m, Sandbag Lunges 100m, Wall Balls 100 reps (target 3.00m
// men, 2.70m women).

// Women's Open
const (
	HyroxWomenOpenSledPushKg = 102.0 // 50kg + 52kg sled
	HyroxWomenOpenSledPullKg = 78.0  // 25kg + 53kg sled
	HyroxWomenOpenFarmersKg  = 16.0  // Per hand (2x16kg)
	HyroxWomenOpenSandbagKg  = 10.0  // Sandbag
	HyroxWomenOpenWallBallKg = 4.0   // Wall Ball
)

// Women's Pro
const (
	HyroxWomenProSledPushKg = 152.0 // 100kg + 52kg sled
	HyroxWomenProSledPullKg = 103.0 // 50kg + 53kg sled
	HyroxWomenProFarmersKg  = 24.0  // Per hand (2x24kg)
	HyroxWomenProSandbagKg  = 20.0  // Sandbag
	HyroxWomenProWallBallKg = 6.0   // Wall Ball
)

// Men's Open
const (
	HyroxMenOpenSledPushKg = 152.0 // 100kg + 52kg sled
	HyroxMenOpenSledPullKg = 103.0 // 50kg + 53kg sled
	HyroxMenOpenFarmersKg  = 24.0  // Per hand (2x24kg)
	HyroxMenOpenSandbagKg  = 20.0  // Sandbag
	HyroxMenOpenWallBallKg = 6.0   // Wall Ball
)

// Men's Pro
const (
	HyroxMenProSledPushKg = 202.0 // 150kg + 52kg sled
	HyroxMenProSledPullKg = 153.0 // 100kg + 53kg sled
	HyroxMenProFarmersKg  = 32.0  // Per hand (2x32kg)
	HyroxMenProSandbagKg  = 30.0  // Sandbag
	HyroxMenProWallBallKg = 9.0   // Wall Ball
)

// hyroxStations creates the standard Hyrox station order with specified weights
func hyroxStations(sledPush, sledPull, farmers, sandbag, wallBall float64) []Station {
	return []Station{
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "SkiErg", Type: StationTypeCardio, DistanceMeters: 1000, Icon: "⛷️"},
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "Sled Push", Type: StationTypeStrength, DistanceMeters: 50, WeightKg: sledPush, Icon: "🛷"},
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "Sled Pull", Type: StationTypeStrength, DistanceMeters: 50, WeightKg: sledPull, Icon: "🛷"},
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "Burpee Broad Jump", Type: StationTypeStrength, DistanceMeters: 80, Icon: "🏋️"},
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "Rowing (Machine)", Type: StationTypeCardio, DistanceMeters: 1000, Icon: "🚣"},
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "Farmers Carry", Type: StationTypeStrength, DistanceMeters: 200, WeightKg: farmers * 2, Icon: "🧳"},
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "Sandbag Lunges", Type: StationTypeStrength, DistanceMeters: 100, WeightKg: sandbag, Icon: "🎒"},
		{Name: "Running (Outdoor)", Type: StationTypeRun, DistanceMeters: 1000, Icon: "🏃"},
		{Name: "Wall Balls", Type: StationTypeStrength, Reps: 100, WeightKg: wallBall, Icon: "🏐"},
	}
}

// RacePresets contains all available hybrid race presets
var RacePresets = map[string]RacePreset{
	// Women's presets
	"hyrox_women_open": {
		ID:          "hyrox_women_open",
		Name:        "Hyrox - Women's Open",
		Description: "Standard Hyrox race for women (Open division)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxWomenOpenSledPushKg, HyroxWomenOpenSledPullKg, HyroxWomenOpenFarmersKg, HyroxWomenOpenSandbagKg, HyroxWomenOpenWallBallKg),
	},
	"hyrox_women_pro": {
		ID:          "hyrox_women_pro",
		Name:        "Hyrox - Women's Pro",
		Description: "Hyrox race for women (Pro division)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxWomenProSledPushKg, HyroxWomenProSledPullKg, HyroxWomenProFarmersKg, HyroxWomenProSandbagKg, HyroxWomenProWallBallKg),
	},
	// Men's presets
	"hyrox_men_open": {
		ID:          "hyrox_men_open",
		Name:        "Hyrox - Men's Open",
		Description: "Standard Hyrox race for men (Open division)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxMenOpenSledPushKg, HyroxMenOpenSledPullKg, HyroxMenOpenFarmersKg, HyroxMenOpenSandbagKg, HyroxMenOpenWallBallKg),
	},
	"hyrox_men_pro": {
		ID:          "hyrox_men_pro",
		Name:        "Hyrox - Men's Pro",
		Description: "Hyrox race for men (Pro division)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxMenProSledPushKg, HyroxMenProSledPullKg, HyroxMenProFarmersKg, HyroxMenProSandbagKg, HyroxMenProWallBallKg),
	},
	// Doubles presets. Doubles race at the standards of the matching solo division (the
	// two partners split each station's work; distances and reps are unchanged): Open
	// Doubles at the Open weights, Pro Doubles at the Pro weights. Mixed Doubles runs at
	// the Men's Open weights on every station except Wall Balls, where each partner throws
	// their own division's ball (men 6kg / 3.00m, women 4kg / 2.70m); the preset records
	// the men's 6kg because a set carries one weight.
	"hyrox_doubles_women": {
		ID:          "hyrox_doubles_women",
		Name:        "Hyrox - Women's Doubles",
		Description: "Hyrox doubles race for women (Women's Open weights)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxWomenOpenSledPushKg, HyroxWomenOpenSledPullKg, HyroxWomenOpenFarmersKg, HyroxWomenOpenSandbagKg, HyroxWomenOpenWallBallKg),
	},
	"hyrox_doubles_men": {
		ID:          "hyrox_doubles_men",
		Name:        "Hyrox - Men's Doubles",
		Description: "Hyrox doubles race for men (Men's Open weights)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxMenOpenSledPushKg, HyroxMenOpenSledPullKg, HyroxMenOpenFarmersKg, HyroxMenOpenSandbagKg, HyroxMenOpenWallBallKg),
	},
	"hyrox_doubles_women_pro": {
		ID:          "hyrox_doubles_women_pro",
		Name:        "Hyrox - Women's Pro Doubles",
		Description: "Hyrox pro doubles race for women (Women's Pro weights)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxWomenProSledPushKg, HyroxWomenProSledPullKg, HyroxWomenProFarmersKg, HyroxWomenProSandbagKg, HyroxWomenProWallBallKg),
	},
	"hyrox_doubles_men_pro": {
		ID:          "hyrox_doubles_men_pro",
		Name:        "Hyrox - Men's Pro Doubles",
		Description: "Hyrox pro doubles race for men (Men's Pro weights)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxMenProSledPushKg, HyroxMenProSledPullKg, HyroxMenProFarmersKg, HyroxMenProSandbagKg, HyroxMenProWallBallKg),
	},
	"hyrox_mixed_doubles": {
		ID:          "hyrox_mixed_doubles",
		Name:        "Hyrox - Mixed Doubles",
		Description: "Hyrox race with mixed gender partner (Men's Open weights; own-gender wall balls)",
		RaceType:    "hyrox",
		Stations:    hyroxStations(HyroxMenOpenSledPushKg, HyroxMenOpenSledPullKg, HyroxMenOpenFarmersKg, HyroxMenOpenSandbagKg, HyroxMenOpenWallBallKg),
	},
}

// GetPresetList returns a list of preset IDs and names for the UI
func GetPresetList() []struct {
	ID   string
	Name string
} {
	return []struct {
		ID   string
		Name string
	}{
		// Women's categories
		{ID: "hyrox_women_open", Name: "Hyrox - Women's Open"},
		{ID: "hyrox_women_pro", Name: "Hyrox - Women's Pro"},
		// Men's categories
		{ID: "hyrox_men_open", Name: "Hyrox - Men's Open"},
		{ID: "hyrox_men_pro", Name: "Hyrox - Men's Pro"},
		// Doubles categories
		{ID: "hyrox_doubles_women", Name: "Hyrox - Women's Doubles"},
		{ID: "hyrox_doubles_men", Name: "Hyrox - Men's Doubles"},
		{ID: "hyrox_mixed_doubles", Name: "Hyrox - Mixed Doubles"},
		{ID: "hyrox_doubles_women_pro", Name: "Hyrox - Women's Pro Doubles"},
		{ID: "hyrox_doubles_men_pro", Name: "Hyrox - Men's Pro Doubles"},
	}
}

// GetPreset retrieves a preset by ID
func GetPreset(id string) (RacePreset, bool) {
	preset, ok := RacePresets[id]
	return preset, ok
}
