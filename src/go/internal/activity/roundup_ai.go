package activity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/claude"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"github.com/fitglue/server/src/go/pkg/voice"
)

const roundupAISummaryTimeout = 120 * time.Second

const maxRoundupSummaryChars = 900

const (
	maxRoundupPhotos  = 24
	maxRoundupRoutes  = 12
	maxRoundupPlaces  = 8
	maxRoundupMuscles = 6
)

// wetWeatherKeywords flag a weather description as "grit" weather for the roundup.
var wetWeatherKeywords = []string{"rain", "drizzle", "shower", "snow", "sleet", "thunder"}

func isWetWeather(desc string) bool {
	d := strings.ToLower(desc)
	for _, k := range wetWeatherKeywords {
		if strings.Contains(d, k) {
			return true
		}
	}
	return false
}

// computeSessionPeaks finds the single-session maxima across the period for the
// "ceiling raised" highlights band: furthest distance, most calories in one
// session, and the heaviest strength session (reps × weight volume).
func computeSessionPeaks(entries []*pbactivity.ShowcaseProfileEntry) (
	furthestM float64, mostCal int32, biggestVolKg float64,
	furthestID, mostCalID, biggestVolID string,
) {
	for _, e := range entries {
		if e.DistanceMeters > furthestM {
			furthestM = e.DistanceMeters
			furthestID = e.ShowcaseId
		}
		if e.CaloriesKcal != nil && *e.CaloriesKcal > mostCal {
			mostCal = *e.CaloriesKcal
			mostCalID = e.ShowcaseId
		}
		if e.TotalWeightKg > biggestVolKg {
			biggestVolKg = e.TotalWeightKg
			biggestVolID = e.ShowcaseId
		}
	}
	return furthestM, mostCal, biggestVolKg, furthestID, mostCalID, biggestVolID
}

// shortLocationName trims a stored location label down to its leading, most-specific
// component for the roundup "where it happened" list. Location names can arrive as full
// reverse-geocoded strings ("Fernwood CP, Newark and Sherwood, Nottinghamshire, East
// Midlands, England, United Kingdom") — correct, but far too wordy for a place chip. The
// roundup only wants the locality ("Fernwood CP"), so we keep everything up to the first
// comma. Already-short names ("West Bridgford", "Richmond Park") have no comma and pass
// through unchanged, so this is safe and idempotent across geocoder versions.
func shortLocationName(name string) string {
	if i := strings.IndexByte(name, ','); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

// aggregateRoundupEnrichments rolls the per-entry enrichment summaries up into
// the period-level place list, weather grit, best efforts and muscles worked.
func aggregateRoundupEnrichments(entries []*pbactivity.ShowcaseProfileEntry) (
	places []*pbactivity.RoundupPlace,
	weather *pbactivity.RoundupWeather,
	bestEfforts []*pbactivity.BestEffort,
	muscles []*pbactivity.RoundupMuscle,
) {
	placeData := map[string]*pbactivity.RoundupPlace{}
	var placeOrder []string
	muscleData := map[string]*pbactivity.RoundupMuscle{}
	var muscleOrder []string
	effortByKey := map[string]*pbactivity.BestEffort{}
	var effortOrder []string

	var w pbactivity.RoundupWeather
	weatherSeen, coldSet, hotSet := false, false, false

	for _, e := range entries {
		// Places trained. Names are shortened to their leading locality so the
		// "where it happened" chips stay concise, and deduped on that short form
		// so the same locality (however verbosely stored) rolls up into one chip.
		if e.LocationName != nil {
			if name := shortLocationName(*e.LocationName); name != "" {
				p, ok := placeData[name]
				if !ok {
					p = &pbactivity.RoundupPlace{Name: name}
					if e.Country != nil {
						p.Country = *e.Country
					}
					placeData[name] = p
					placeOrder = append(placeOrder, name)
				}
				p.ActivityCount++
			}
		}

		// Weather grit
		if e.TempC != nil {
			weatherSeen = true
			w.SessionCount++
			t := *e.TempC
			if !coldSet || t < w.ColdestTempC {
				w.ColdestTempC = t
				coldSet = true
			}
			if !hotSet || t > w.HottestTempC {
				w.HottestTempC = t
				hotSet = true
			}
		}
		if e.WeatherDescription != nil && isWetWeather(*e.WeatherDescription) {
			w.RainCount++
		}

		// Best efforts — fastest per distance key
		for _, be := range e.BestEfforts {
			if be == nil || be.DistanceKey == "" || be.TimeSeconds <= 0 {
				continue
			}
			cur, ok := effortByKey[be.DistanceKey]
			if !ok {
				effortByKey[be.DistanceKey] = be
				effortOrder = append(effortOrder, be.DistanceKey)
			} else if be.TimeSeconds < cur.TimeSeconds {
				effortByKey[be.DistanceKey] = be
			}
		}

		// Muscles worked
		for _, m := range e.PrimaryMuscles {
			if m == "" {
				continue
			}
			md, ok := muscleData[m]
			if !ok {
				md = &pbactivity.RoundupMuscle{Name: m}
				muscleData[m] = md
				muscleOrder = append(muscleOrder, m)
			}
			md.Count++
		}
	}

	for _, name := range placeOrder {
		places = append(places, placeData[name])
	}
	sort.SliceStable(places, func(i, j int) bool { return places[i].ActivityCount > places[j].ActivityCount })
	if len(places) > maxRoundupPlaces {
		places = places[:maxRoundupPlaces]
	}

	for _, name := range muscleOrder {
		muscles = append(muscles, muscleData[name])
	}
	sort.SliceStable(muscles, func(i, j int) bool { return muscles[i].Count > muscles[j].Count })
	if len(muscles) > maxRoundupMuscles {
		muscles = muscles[:maxRoundupMuscles]
	}

	for _, k := range effortOrder {
		bestEfforts = append(bestEfforts, effortByKey[k])
	}
	sort.SliceStable(bestEfforts, func(i, j int) bool { return bestEfforts[i].DistanceM < bestEfforts[j].DistanceM })

	if weatherSeen {
		weather = &w
	}
	return places, weather, bestEfforts, muscles
}

// collectRoundupMedia gathers photo and GPS route-thumbnail assets from the
// period's entries for the roundup photo mosaic and route wall. Photos are only
// included when the owner has the public photo gallery enabled; both lists are
// capped to keep the persisted roundup document small.
func collectRoundupMedia(entries []*pbactivity.ShowcaseProfileEntry, galleryEnabled bool) (photos []*pbactivity.RoundupPhoto, routes []*pbactivity.RoundupRoute) {
	for _, e := range entries {
		dateStr := ""
		if e.StartTime != nil {
			dateStr = e.StartTime.AsTime().UTC().Format("2 Jan")
		}
		if galleryEnabled && len(photos) < maxRoundupPhotos {
			for _, url := range e.PhotoUrls {
				if url == "" {
					continue
				}
				photos = append(photos, &pbactivity.RoundupPhoto{
					Url:           url,
					ActivityTitle: e.Title,
					Date:          dateStr,
					ActivityType:  e.ActivityType,
					ShowcaseId:    e.ShowcaseId,
				})
				if len(photos) >= maxRoundupPhotos {
					break
				}
			}
		}
		if e.RouteThumbnailUrl != "" && len(routes) < maxRoundupRoutes {
			routes = append(routes, &pbactivity.RoundupRoute{
				ThumbnailUrl:   e.RouteThumbnailUrl,
				ActivityTitle:  e.Title,
				Date:           dateStr,
				DistanceMeters: e.DistanceMeters,
				ActivityType:   e.ActivityType,
				ShowcaseId:     e.ShowcaseId,
			})
		}
	}
	return photos, routes
}

// deriveEffortLevel maps a single showcase entry to a 0–4 effort level for
// the consistency calendar heatmap (0=rest,1=easy,2=moderate,3=hard,4=peak).
func deriveEffortLevel(e *pbactivity.ShowcaseProfileEntry) int32 {
	if len(e.HrZoneMinutes) >= 6 {
		var total int32
		for i := 1; i <= 5; i++ {
			total += e.HrZoneMinutes[i]
		}
		if total > 10 {
			z5 := e.HrZoneMinutes[5]
			z45 := e.HrZoneMinutes[4] + z5
			z12 := e.HrZoneMinutes[1] + e.HrZoneMinutes[2]
			switch {
			case z5*100 > total*15:
				return 4 // >15 % in Z5 → peak
			case z45*100 > total*25:
				return 3 // >25 % in Z4+Z5 → hard
			case z12*100 > total*70:
				return 1 // >70 % in Z1+Z2 → easy
			default:
				return 2
			}
		}
	}
	// duration-based fallback
	mins := int32(e.DurationSeconds / 60)
	switch {
	case mins >= 120:
		return 4
	case mins >= 75:
		return 3
	case mins >= 30:
		return 2
	case mins > 0:
		return 1
	default:
		return 0
	}
}

// generateRoundupAISummary asks Claude Sonnet for the roundup write-up in
// James's voice. It returns "" (the roundup keeps its non-AI text) when Claude
// errors or is limited, or the output fails voice.Line/voice.Check. Raw model
// output is never returned.
func generateRoundupAISummary(ctx context.Context, logger infra.Logger, ai claude.Completer, roundup *pbactivity.ShowcaseRoundup) string {
	if ai == nil {
		ai = claude.Default()
	}
	text, attempts, err := voice.Generate(func(extra string) (string, error) {
		sys := voice.Guide() + "\n\n" + roundupTaskBlock
		if extra != "" {
			sys += "\n\n" + extra
		}
		return ai.Complete(ctx, claude.Request{
			Model:   claude.ModelSonnet,
			System:  sys,
			Prompt:  roundupUserPrompt + "\n\nPeriod data:\n" + buildRoundupSummaryContext(roundup),
			Timeout: roundupAISummaryTimeout,
		})
	}, maxRoundupSummaryChars, roundupCheckContext(roundup))
	if err != nil {
		logger.Warn(ctx, "roundup AI summary: Claude unavailable", "error", err, "limited", errors.Is(err, claude.ErrLimited))
		return ""
	}
	if text == "" {
		logger.Warn(ctx, "roundup AI summary: output failed the voice check twice, dropped")
	} else if attempts == 2 {
		logger.Info(ctx, "roundup AI summary: passed the voice check on retry")
	}
	return text
}

const roundupTaskBlock = `Write the roundup for this period: ONE paragraph of 4-6 sentences, in the voice above.
- Find a through-line built from the numbers (a sport that dominated, a streak that held, strength that quietly climbed).
- Use only facts in the data. Never invent numbers, sports or events.
- No names at all: not his, not anyone's, no places. Never quote an activity title. No 'you'. No Markdown, no lists.
- Never mention alcohol, drink, drugs, pubs or recovery.
- No emoji.
- End on a line worth screenshotting.`

const roundupUserPrompt = `Write the roundup paragraph now.`

// calendarWords are weekday and month names: date vocabulary the prompt itself
// carries, not names (voice.Check already allowlists them).
const calendarWords = "Monday Tuesday Wednesday Thursday Friday Saturday Sunday January February March April May June July August September October November December"

// roundupCheckContext is the only context voice.Check sees for a roundup:
// fitness vocabulary (period, sport labels, best-effort labels, muscles). Never
// an activity title, so a word from his own text cannot whitelist itself.
func roundupCheckContext(roundup *pbactivity.ShowcaseRoundup) string {
	parts := []string{periodTypeName(roundup.PeriodType), calendarWords}
	for _, bd := range roundup.ActivityTypeBreakdowns {
		parts = append(parts, activityTypeShortLabel(bd.ActivityType))
	}
	for _, be := range roundup.BestEfforts {
		parts = append(parts, be.Display, be.DistanceKey)
	}
	for _, m := range roundup.Muscles {
		parts = append(parts, m.Name)
	}
	return strings.Join(parts, " ")
}

// sanitiseRoundupString truncates user-controlled strings and strips prompt-injection
// delimiters before they are embedded in the prompt, scrubbed, and dropped if sensitive.
func sanitiseRoundupString(s string) string {
	const maxLen = 200
	if voice.Sensitive(s) {
		return ""
	}
	s = voice.Scrub(strings.NewReplacer("<", "", ">", "", "{", "", "}", "").Replace(s))
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return strings.TrimSpace(s)
}

// buildRoundupSummaryContext produces the plaintext data section of the
// prompt from an already-computed ShowcaseRoundup.
func buildRoundupSummaryContext(roundup *pbactivity.ShowcaseRoundup) string {
	var lines []string

	// Period
	period := periodTypeName(roundup.PeriodType)
	lines = append(lines, fmt.Sprintf("Period: %s", period))
	if roundup.PeriodStart != nil && roundup.PeriodEnd != nil {
		start := roundup.PeriodStart.AsTime().UTC().Format("2 Jan 2006")
		end := roundup.PeriodEnd.AsTime().UTC().AddDate(0, 0, -1).Format("2 Jan 2006")
		lines = append(lines, fmt.Sprintf("Date range: %s – %s", start, end))
	}

	lines = append(lines, fmt.Sprintf("Sessions: %d", roundup.TotalActivities))

	if roundup.TotalDurationSeconds > 0 {
		h := int(roundup.TotalDurationSeconds) / 3600
		m := (int(roundup.TotalDurationSeconds) % 3600) / 60
		if h > 0 {
			lines = append(lines, fmt.Sprintf("Total time: %dh %dm", h, m))
		} else {
			lines = append(lines, fmt.Sprintf("Total time: %dm", m))
		}
	}

	if roundup.TotalDistanceMeters > 500 {
		km := roundup.TotalDistanceMeters / 1000
		lines = append(lines, fmt.Sprintf("Total distance: %.1f km", km))
	}

	if roundup.TotalCaloriesKcal > 0 {
		lines = append(lines, fmt.Sprintf("Calories burned: %d kcal", roundup.TotalCaloriesKcal))
	}

	// Sport breakdown
	if len(roundup.ActivityTypeBreakdowns) > 0 {
		var sports []string
		for _, bd := range roundup.ActivityTypeBreakdowns {
			label := activityTypeShortLabel(bd.ActivityType)
			sports = append(sports, fmt.Sprintf("%d %s", bd.ActivityCount, label))
		}
		lines = append(lines, fmt.Sprintf("Sports: %s", strings.Join(sports, ", ")))
	}

	if len(roundup.PrsAchieved) > 0 {
		lines = append(lines, fmt.Sprintf("Personal records broken: %d", len(roundup.PrsAchieved)))
	}

	if roundup.LongestActivityDurationSeconds > 60 {
		h := int(roundup.LongestActivityDurationSeconds) / 3600
		m := (int(roundup.LongestActivityDurationSeconds) % 3600) / 60
		if h > 0 {
			lines = append(lines, fmt.Sprintf("Longest session: %dh %dm", h, m))
		} else {
			lines = append(lines, fmt.Sprintf("Longest session: %dm", m))
		}
	}

	if roundup.HighestAvgBpm > 0 {
		if title := sanitiseRoundupString(roundup.HighestAvgBpmActivityTitle); title != "" {
			lines = append(lines, fmt.Sprintf("Peak avg heart rate: %d bpm (%s)", roundup.HighestAvgBpm, title))
		} else {
			lines = append(lines, fmt.Sprintf("Peak avg heart rate: %d bpm", roundup.HighestAvgBpm))
		}
	}

	if roundup.HighestCaloriesPerHourKcal > 0 {
		lines = append(lines, fmt.Sprintf("Highest burn rate: %.0f kcal/h", math.Round(roundup.HighestCaloriesPerHourKcal)))
	}

	// Effort distribution
	easy := roundup.EffortEasyCount
	mod := roundup.EffortModerateCount
	hard := roundup.EffortHardCount
	if easy+mod+hard > 0 {
		lines = append(lines, fmt.Sprintf("Effort distribution: %d easy, %d moderate, %d hard", easy, mod, hard))
	}

	// Standout moments (pre-computed callouts)
	for _, c := range roundup.CalloutActivities {
		kind := strings.ToUpper(c.Kind)
		switch {
		case strings.Contains(kind, "STREAK"):
			if title := sanitiseRoundupString(c.Title); title != "" {
				lines = append(lines, fmt.Sprintf("Longest streak: %s", title))
			}
		case strings.Contains(kind, "BIGGEST"):
			if title := sanitiseRoundupString(c.Title); title != "" {
				lines = append(lines, fmt.Sprintf("Biggest single session: %s (%s)", title, sanitiseRoundupString(c.StatValue)))
			}
		}
	}

	if roundup.TotalElevationGainMeters > 50 {
		lines = append(lines, fmt.Sprintf("Total elevation gain: %.0f m", roundup.TotalElevationGainMeters))
	}

	// Best efforts
	if len(roundup.BestEfforts) > 0 {
		var efforts []string
		for _, be := range roundup.BestEfforts {
			label := sanitiseRoundupString(be.Display)
			if label == "" {
				label = sanitiseRoundupString(be.DistanceKey)
			}
			if label != "" && be.TimeSeconds > 0 {
				efforts = append(efforts, fmt.Sprintf("%s in %s", label, formatEffortClock(be.TimeSeconds)))
			}
		}
		if len(efforts) > 0 {
			lines = append(lines, fmt.Sprintf("Best efforts: %s", strings.Join(efforts, ", ")))
		}
	}

	// Muscles worked most (strength)
	if len(roundup.Muscles) > 0 {
		var ms []string
		for _, m := range roundup.Muscles {
			if n := sanitiseRoundupString(m.Name); n != "" {
				ms = append(ms, n)
			}
		}
		if len(ms) > 0 {
			lines = append(lines, fmt.Sprintf("Most-worked muscles: %s", strings.Join(ms, ", ")))
		}
	}

	return strings.Join(lines, "\n")
}

// formatEffortClock renders a duration in seconds as m:ss (or h:mm:ss).
func formatEffortClock(sec float64) string {
	s := int(sec)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, (s%3600)/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func periodTypeName(t pbactivity.RoundupPeriodType) string {
	switch t {
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK:
		return "week"
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH:
		return "month"
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR:
		return "year"
	default:
		return "period"
	}
}

func activityTypeShortLabel(t pbactivity.ActivityType) string {
	s := t.String()
	s = strings.TrimPrefix(s, "ACTIVITY_TYPE_")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", " ")
	return s
}
