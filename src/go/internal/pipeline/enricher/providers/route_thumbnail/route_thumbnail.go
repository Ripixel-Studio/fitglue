package route_thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"log/slog"
	"math"
	"os"
	"text/template"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// RouteThumbnailProvider generates a static map SVG image of the GPS route
// Produces a route thumbnail asset for Showcase
type RouteThumbnailProvider struct {
	service *bootstrap.Service
}

func init() {
	providers.Register(NewRouteThumbnailProvider())
}

func NewRouteThumbnailProvider() *RouteThumbnailProvider {
	return &RouteThumbnailProvider{}
}

func (p *RouteThumbnailProvider) SetService(service *bootstrap.Service) {
	p.service = service
}

func (p *RouteThumbnailProvider) Name() string {
	return "route_thumbnail"
}

func (p *RouteThumbnailProvider) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_ROUTE_THUMBNAIL
}

// GPSPoint represents a single GPS coordinate
type GPSPoint struct {
	Lat  float64
	Long float64
}

func (p *RouteThumbnailProvider) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputConfig map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	// Extract GPS points from all records
	var points []GPSPoint
	for _, session := range activity.Sessions {
		for _, lap := range session.Laps {
			for _, record := range lap.Records {
				if record.PositionLat != 0 && record.PositionLong != 0 {
					points = append(points, GPSPoint{
						Lat:  record.PositionLat,
						Long: record.PositionLong,
					})
				}
			}
		}
	}

	// Require at least 10 GPS points for a reasonable route
	if len(points) < 10 {
		logger.Info("Skipping route thumbnail - insufficient GPS data", "points", len(points))
		return &providers.EnrichmentResult{Skipped: true, SkipReason: "Insufficient GPS data"}, nil
	}

	// Simplify the route if too many points (Douglas-Peucker)
	if len(points) > 200 {
		points = simplifyRoute(points, 0.0001) // ~10m tolerance
	}

	// Generate SVG
	svgContent := generateRouteSVG(points)

	// Upload to GCS - use dedicated showcase assets bucket
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

	objectPath := fmt.Sprintf("%s/route-thumbnail.svg", assetFolderID)
	if err := p.service.Store.Write(ctx, bucketName, objectPath, []byte(svgContent)); err != nil {
		return nil, fmt.Errorf("failed to upload SVG to GCS: %w", err)
	}

	// Build URL using custom domain if configured, otherwise raw GCS URL
	// ASSETS_BASE_URL should be set per environment:
	//   - Dev: https://assets.dev.fitglue.tech
	//   - Prod: https://assets.fitglue.tech
	assetsBaseURL := os.Getenv("ASSETS_BASE_URL")
	var assetURL string
	if assetsBaseURL != "" {
		assetURL = fmt.Sprintf("%s/%s", assetsBaseURL, objectPath)
	} else {
		// Fallback to raw GCS URL
		assetURL = fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectPath)
	}

	logger.Info("Generated route thumbnail", "asset_folder_id", assetFolderID, "url", assetURL, "points", len(points))

	return &providers.EnrichmentResult{
		Metadata: map[string]string{
			"asset_route_thumbnail": assetURL,
		},
	}, nil
}

// generateRouteSVG creates an SVG visualization of the GPS route
func generateRouteSVG(points []GPSPoint) string {
	// Find bounding box
	minLat, maxLat := points[0].Lat, points[0].Lat
	minLong, maxLong := points[0].Long, points[0].Long

	for _, p := range points {
		if p.Lat < minLat {
			minLat = p.Lat
		}
		if p.Lat > maxLat {
			maxLat = p.Lat
		}
		if p.Long < minLong {
			minLong = p.Long
		}
		if p.Long > maxLong {
			maxLong = p.Long
		}
	}

	// Canvas dimensions with margin
	width := 400.0
	height := 400.0
	margin := 30.0 // Padding from canvas edges

	// Available drawing area (inside margins)
	drawWidth := width - 2*margin
	drawHeight := height - 2*margin

	// Calculate scale to fit route within drawing area while maintaining aspect ratio
	latRange := maxLat - minLat
	longRange := maxLong - minLong

	// Scale based on whichever dimension constrains us
	scaleX := drawWidth / longRange
	scaleY := drawHeight / latRange
	scale := math.Min(scaleX, scaleY)

	// Calculate actual route dimensions after scaling
	routeWidth := longRange * scale
	routeHeight := latRange * scale

	// Calculate offsets to center the route within the drawing area
	offsetX := margin + (drawWidth-routeWidth)/2
	offsetY := margin + (drawHeight-routeHeight)/2

	// Project points to SVG coordinates
	var svgPoints []struct {
		X float64
		Y float64
	}

	for _, p := range points {
		// Longitude is X, Latitude is Y (inverted because SVG Y increases downward)
		x := offsetX + (p.Long-minLong)*scale
		y := offsetY + (maxLat-p.Lat)*scale // Invert Y: higher lat = lower Y value
		svgPoints = append(svgPoints, struct {
			X float64
			Y float64
		}{X: x, Y: y})
	}

	// Build path data
	pathData := fmt.Sprintf("M%.2f,%.2f", svgPoints[0].X, svgPoints[0].Y)
	for i := 1; i < len(svgPoints); i++ {
		pathData += fmt.Sprintf(" L%.2f,%.2f", svgPoints[i].X, svgPoints[i].Y)
	}

	// Start and end points
	startX, startY := svgPoints[0].X, svgPoints[0].Y
	endX, endY := svgPoints[len(svgPoints)-1].X, svgPoints[len(svgPoints)-1].Y

	// Generate SVG
	data := struct {
		Width    float64
		Height   float64
		PathData string
		StartX   float64
		StartY   float64
		EndX     float64
		EndY     float64
	}{
		Width:    width,
		Height:   height,
		PathData: pathData,
		StartX:   startX,
		StartY:   startY,
		EndX:     endX,
		EndY:     endY,
	}

	tmpl := template.Must(template.New("route").Parse(RouteSVGTemplate))
	var buf bytes.Buffer
	tmpl.Execute(&buf, data)

	return buf.String()
}

// simplifyRoute uses Douglas-Peucker algorithm to reduce points
func simplifyRoute(points []GPSPoint, tolerance float64) []GPSPoint {
	if len(points) < 3 {
		return points
	}

	// Find the point with the maximum distance from the line between first and last
	maxDist := 0.0
	maxIdx := 0

	first := points[0]
	last := points[len(points)-1]

	for i := 1; i < len(points)-1; i++ {
		dist := perpendicularDistance(points[i], first, last)
		if dist > maxDist {
			maxDist = dist
			maxIdx = i
		}
	}

	// If max distance is greater than tolerance, recursively simplify
	if maxDist > tolerance {
		left := simplifyRoute(points[:maxIdx+1], tolerance)
		right := simplifyRoute(points[maxIdx:], tolerance)
		return append(left[:len(left)-1], right...)
	}

	// Otherwise, return just the endpoints
	return []GPSPoint{first, last}
}

// perpendicularDistance calculates distance from point to line segment
func perpendicularDistance(point, lineStart, lineEnd GPSPoint) float64 {
	dx := lineEnd.Long - lineStart.Long
	dy := lineEnd.Lat - lineStart.Lat

	if dx == 0 && dy == 0 {
		// Line is a point
		return math.Sqrt(math.Pow(point.Long-lineStart.Long, 2) + math.Pow(point.Lat-lineStart.Lat, 2))
	}

	// Normalized perpendicular distance
	t := ((point.Long-lineStart.Long)*dx + (point.Lat-lineStart.Lat)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))

	closestX := lineStart.Long + t*dx
	closestY := lineStart.Lat + t*dy

	return math.Sqrt(math.Pow(point.Long-closestX, 2) + math.Pow(point.Lat-closestY, 2))
}
