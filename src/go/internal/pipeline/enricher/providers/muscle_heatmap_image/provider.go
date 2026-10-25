package muscle_heatmap_image

import (
	"bytes"
	"context"
	"fmt"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/muscle_heatmap"
	"github.com/fitglue/server/src/go/pkg/bootstrap"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// MuscleHeatmapImageProvider generates an anatomical SVG heatmap of muscle activation
// Produces a visual asset for Showcase
type MuscleHeatmapImageProvider struct {
	service *bootstrap.Service
}

func init() {
	providers.Register(NewMuscleHeatmapImageProvider())
}

func NewMuscleHeatmapImageProvider() *MuscleHeatmapImageProvider {
	return &MuscleHeatmapImageProvider{}
}

func (p *MuscleHeatmapImageProvider) SetService(svc *bootstrap.Service) {
	p.service = svc
}

func (p *MuscleHeatmapImageProvider) Name() string {
	return "muscle-heatmap-image"
}

func (p *MuscleHeatmapImageProvider) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_MUSCLE_HEATMAP_IMAGE
}

func (p *MuscleHeatmapImageProvider) ShouldDefer() bool {
	return true
}

// MuscleScore represents activation level for a muscle group
type MuscleScore struct {
	SVGIDs     []string // One or more SVG IDs to target
	Percentage float64  // 0.0 to 1.0
	Color      string   // Hex color
}

func (p *MuscleHeatmapImageProvider) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputConfig map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	logger.Debug("muscle_heatmap_image: starting", "activity_name", activity.Name)
	// Aggregate strength sets
	var allSets []*pbactivity.StrengthSet
	for _, s := range activity.Sessions {
		allSets = append(allSets, s.StrengthSets...)
	}

	if len(allSets) == 0 {
		return &providers.EnrichmentResult{Skipped: true, SkipReason: "No strength sets found"}, nil
	}

	// Apply coefficient preset (same options as text-based heatmap)
	coeffs := muscle_heatmap.StandardCoefficients
	if preset, ok := inputConfig["preset"]; ok {
		coeffs = muscle_heatmap.GetPresetCoefficients(preset)
	}

	// Determine gender (default to male/man)
	gender := "man"
	if g, ok := inputConfig["gender"]; ok && g != "" {
		g = strings.ToLower(g)
		if g == "female" || g == "woman" {
			gender = "woman"
		}
	}

	// Calculate muscle activation scores
	scores := p.calculateMuscleScores(allSets, coeffs)
	if len(scores) == 0 {
		return &providers.EnrichmentResult{Skipped: true, SkipReason: "No muscle scores calculated"}, nil
	}

	// Generate SVG
	svgContent, err := p.GenerateSVG(gender, scores)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SVG: %w", err)
	}

	// Store in Cloud Storage if service is available
	var assetURL string
	if p.service != nil && p.service.Store != nil {
		// Use dedicated showcase assets bucket
		bucketName := os.Getenv("SHOWCASE_ASSETS_BUCKET")
		if bucketName == "" {
			bucketName = "fitglue-server-dev-showcase-assets" // Fallback for local development
		}

		// Use pipeline_execution_id for asset storage path (unique per pipeline execution)
		// Falls back to activity.ExternalId for backward compatibility
		assetFolderID := inputConfig["pipeline_execution_id"]
		if assetFolderID == "" {
			assetFolderID = activity.ExternalId
		}
		if assetFolderID == "" {
			assetFolderID = "unknown"
		}

		objectPath := fmt.Sprintf("%s/muscle-heatmap.svg", assetFolderID)
		if err := p.service.Store.Write(ctx, bucketName, objectPath, []byte(svgContent)); err != nil {
			logger.Warn("Failed to store SVG asset", "error", err)
		} else {
			// Build URL using custom domain if configured, otherwise raw GCS URL
			// ASSETS_BASE_URL should be set per environment:
			//   - Dev: https://assets.dev.fitglue.tech
			//   - Prod: https://assets.fitglue.tech
			assetsBaseURL := os.Getenv("ASSETS_BASE_URL")
			if assetsBaseURL != "" {
				assetURL = fmt.Sprintf("%s/%s", assetsBaseURL, objectPath)
			} else {
				// Fallback to raw GCS URL
				assetURL = fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectPath)
			}
		}
	}

	metadata := map[string]string{
		"muscle_groups_activated": fmt.Sprintf("%d", len(scores)),
	}

	if assetURL != "" {
		metadata["asset_muscle_heatmap"] = assetURL
	}

	return &providers.EnrichmentResult{
		Metadata: metadata,
	}, nil
}

func (p *MuscleHeatmapImageProvider) calculateMuscleScores(sets []*pbactivity.StrengthSet, coeffs map[pbactivity.MuscleGroup]float64) []MuscleScore {
	volumeScores := make(map[pbactivity.MuscleGroup]float64)
	maxScore := 0.0

	for _, set := range sets {
		primary := set.PrimaryMuscleGroup
		secondary := set.SecondaryMuscleGroups
		load := muscle_heatmap.CalculateLoad(set)

		// Fallback to taxonomy lookup if muscle is unspecified
		if primary == pbactivity.MuscleGroup_MUSCLE_GROUP_UNSPECIFIED || primary == pbactivity.MuscleGroup_MUSCLE_GROUP_OTHER {
			result := muscle_heatmap.LookupExercise(set.ExerciseName)
			if result.Matched {
				primary = result.Primary
				secondary = result.Secondary
			}
		}

		if primary != pbactivity.MuscleGroup_MUSCLE_GROUP_UNSPECIFIED && primary != pbactivity.MuscleGroup_MUSCLE_GROUP_OTHER {
			coeff := muscle_heatmap.GetMuscleCoefficient(coeffs, primary)
			volumeScores[primary] += load * coeff
			if volumeScores[primary] > maxScore {
				maxScore = volumeScores[primary]
			}
		}

		// Secondary muscles at 50% impact
		for _, sec := range secondary {
			if sec != pbactivity.MuscleGroup_MUSCLE_GROUP_UNSPECIFIED && sec != pbactivity.MuscleGroup_MUSCLE_GROUP_OTHER {
				coeff := muscle_heatmap.GetMuscleCoefficient(coeffs, sec)
				volumeScores[sec] += load * coeff * 0.5
				if volumeScores[sec] > maxScore {
					maxScore = volumeScores[sec]
				}
			}
		}
	}

	// Convert to percentage-based scores
	var scores []MuscleScore
	for muscle, score := range volumeScores {
		if score > 0 {
			svgIDs := getMuscleSVGIDs(muscle)
			// Skip muscles with no SVG representation (e.g., FULL_BODY, CARDIO)
			if len(svgIDs) == 0 {
				continue
			}
			pct := score / maxScore
			scores = append(scores, MuscleScore{
				SVGIDs:     svgIDs,
				Percentage: pct,
				Color:      percentageToColor(pct),
			})
		}
	}

	// Sort by percentage descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Percentage > scores[j].Percentage
	})

	return scores
}

func getMuscleSVGIDs(m pbactivity.MuscleGroup) []string {
	switch m {
	case pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST:
		return []string{"pectoralis_major"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_SHOULDERS:
		return []string{"deltoid", "infraspinatus_teres_major"} // Shoulders includes rear delts/rotator cuff
	case pbactivity.MuscleGroup_MUSCLE_GROUP_BICEPS:
		return []string{"biceps"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS:
		return []string{"triceps"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_FOREARMS:
		return []string{"brachioradialis", "finger_flexors", "finger_extensors"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_LATS:
		return []string{"latissimus_dorsi"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_UPPER_BACK:
		return []string{"traps", "trapezius", "trapezius_lower", "infraspinatus_teres_major"} // Combine various back muscles
	case pbactivity.MuscleGroup_MUSCLE_GROUP_TRAPS:
		return []string{"traps", "trapezius", "trapezius_lower"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_LOWER_BACK:
		// Lower back is covered by latissimus dorsi and external oblique in these SVGs to some extent
		return []string{"latissimus_dorsi", "external_oblique"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_ABDOMINALS:
		return []string{"abdominals", "external_oblique"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_QUADRICEPS:
		return []string{"quadriceps", "sartorius_abductors", "tensor_fasciae_latae"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_HAMSTRINGS:
		return []string{"hamstrings"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_GLUTES:
		return []string{"gluteus_maximus", "gluteus_medius", "gluteus_medius2", "abductors"}
	case pbactivity.MuscleGroup_MUSCLE_GROUP_CALVES:
		return []string{"gastrocnemius", "soleus", "peroneus_longus", "tibialis_anterior"}
	default:
		return nil
	}
}

func percentageToColor(pct float64) string {
	if pct <= 0 {
		return ""
	} else if pct < 0.20 {
		return "#9370DB" // Low: light purple
	} else if pct < 0.40 {
		return "#8B5CF6" // Medium: purple
	} else if pct < 0.60 {
		return "#7C3AED" // High: bright purple
	} else if pct < 0.80 {
		return "#D946EF" // Very High: magenta
	} else {
		return "#EC4899" // Intense: hot pink
	}
}

func (p *MuscleHeatmapImageProvider) GenerateSVG(gender string, scores []MuscleScore) (string, error) {
	// Read front and back SVG templates
	frontPath := fmt.Sprintf("muscle_diagram/%s-front.svg", gender)
	backPath := fmt.Sprintf("muscle_diagram/%s-back.svg", gender)

	frontContent, err := templatesFS.ReadFile(frontPath)
	if err != nil {
		return "", fmt.Errorf("failed to read front template: %w", err)
	}
	backContent, err := templatesFS.ReadFile(backPath)
	if err != nil {
		return "", fmt.Errorf("failed to read back template: %w", err)
	}

	// Calculate centering offsets
	// Canvas halves are 250px wide.
	var frontX, backX, topY float64

	if gender == "woman" {
		// Female Front: ~172px width -> (250 - 172) / 2 = 39
		frontX = 39
		// Female Back: ~154px width -> (250 - 154) / 2 = 48
		backX = 48
		// Vertical centering (approx)
		topY = 20
	} else {
		// Male Front/Back: ~248px width -> (250 - 248) / 2 = 1
		frontX = 1
		backX = 1
		topY = 20
	}

	// Create CSS overlay for heatmap
	var cssBuilder strings.Builder
	cssBuilder.WriteString("<style>\n")
	// Base styles to ensure consistency
	cssBuilder.WriteString(".muscle { cursor: pointer; stroke: #000; stroke-width: 0.5; stroke-opacity: 0.2; }\n")
	// "Bleach" the default colors so the heatmap pops
	cssBuilder.WriteString(".muscle path, .muscle rect, .muscle polygon { fill: #E0E0E0; }\n")

	for _, score := range scores {
		if score.Color == "" {
			continue
		}
		for _, svgID := range score.SVGIDs {
			selector := fmt.Sprintf("#%s, #%s path, #%s rect, #%s polygon", svgID, svgID, svgID, svgID)
			rule := fmt.Sprintf("%s { fill: %s !important; fill-opacity: 0.85; stroke: #fff; stroke-width: 1px; }\n", selector, score.Color)
			cssBuilder.WriteString(rule)
		}
	}
	cssBuilder.WriteString("</style>\n")

	var combinedSVG bytes.Buffer
	combinedSVG.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	combinedSVG.WriteString("\n")
	combinedSVG.WriteString(`<svg version="1.1" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 500 600">`)
	combinedSVG.WriteString("\n")

	// Add FitGlue Marketing Gradient Background
	// Using a more vibrant radial gradient as requested
	combinedSVG.WriteString(`<defs>
		<radialGradient id="fitglue-bg" cx="50%" cy="50%" r="50%" fx="50%" fy="50%">
			<stop offset="0%" style="stop-color:#4a1a4e;stop-opacity:1" />
			<stop offset="100%" style="stop-color:#0a0a0a;stop-opacity:1" />
		</radialGradient>
	</defs>
	<rect width="100%" height="100%" fill="url(#fitglue-bg)"/>`)
	combinedSVG.WriteString("\n")

	// Inject styles
	combinedSVG.WriteString(cssBuilder.String())

	// Front View Group
	combinedSVG.WriteString(fmt.Sprintf(`<g id="front-view" transform="translate(%.2f, %.2f)">`, frontX, topY))
	combinedSVG.Write(extractInnerSVG(frontContent))
	combinedSVG.WriteString("</g>")

	// Back View Group
	// Offset by 250 for the right half, plus the centering offset
	combinedSVG.WriteString(fmt.Sprintf(`<g id="back-view" transform="translate(%.2f, %.2f)">`, 250+backX, topY))
	combinedSVG.Write(extractInnerSVG(backContent))
	combinedSVG.WriteString("</g>")

	// Add Shared Tooltip (must be last to be on top)
	combinedSVG.WriteString(`
<g id="tooltip" visibility="hidden" pointer-events="none">
<rect x="2" y="2" width="80" height="24" fill="black" opacity="0.4" rx="2" ry="2"/>
<rect width="80" height="24" fill="white" rx="2" ry="2"/>
<polygon points="7,0 12,-10 17,0" fill="black" opacity="0.4"></polygon>
<polygon points="5,0 10,-10 15,0" fill="white"></polygon>
<text x="4" y="16" font-family="sans-serif" font-size="10px" fill="black">Tooltip</text>
</g>
<script type="text/ecmascript"><![CDATA[
(function () {
  var svg = document.querySelector('svg');
  var tooltip = svg.getElementById('tooltip');
  var tooltipText = tooltip.getElementsByTagName('text')[0];
  var triggers = svg.getElementsByClassName('tooltip-trigger');

  for (var i = 0; i < triggers.length; i++) {
    triggers[i].addEventListener('mousemove', showTooltip);
    triggers[i].addEventListener('mouseout', hideTooltip);
  }

  function showTooltip(evt) {
    // Get mouse position relative to SVG
    var CTM = svg.getScreenCTM();
    if (!CTM) return;
    var x = (evt.clientX - CTM.e) / CTM.a;
    var y = (evt.clientY - CTM.f) / CTM.d;

    // Offset
    x += 10;
    y += 20;

    tooltip.setAttributeNS(null, "transform", "translate(" + x + " " + y + ")");
    tooltip.setAttributeNS(null, "visibility", "visible");

    // Update text
    var text = evt.target.getAttribute('data-tooltip-text');
    if (text) tooltipText.textContent = text;
  }

  function hideTooltip(evt) {
    tooltip.setAttributeNS(null, "visibility", "hidden");
  }
})()
]]></script>`)

	combinedSVG.WriteString("</svg>")

	return combinedSVG.String(), nil
}

// extractInnerSVG removes the xml header and svg root tag, keeping the content (defs, styles, groups)
func extractInnerSVG(content []byte) []byte {
	s := string(content)
	// Find opening <svg ...>
	start := strings.Index(s, "<svg")
	if start == -1 {
		return content
	}
	// Find end of opening tag >
	closeTagStart := strings.Index(s[start:], ">")
	if closeTagStart == -1 {
		return content
	}
	realStart := start + closeTagStart + 1

	// Find closing </svg>
	end := strings.LastIndex(s, "</svg>")
	if end == -1 {
		return content // Should not happen for valid SVG
	}

	return []byte(s[realStart:end])
}
