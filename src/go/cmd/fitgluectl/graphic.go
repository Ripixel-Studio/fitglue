package main

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// A Strava "photo" is often not a photo: Hevy's auto-posted workout card (white,
// muscle figures, "Volume Lifted"), Hevy's dark class card, a Pixel Watch / Fitbit
// summary screenshot, an app's plan card. isGraphic tells them from real photos
// (including whiteboards and gym floors) on a 64x64 downscale: mostly near-white,
// mostly near-black, or too few colours for a camera image.
const (
	graphicSide       = 64
	graphicWhiteMin   = 240  // every channel above: near-white
	graphicDarkMax    = 35   // every channel below: near-black
	graphicFlatFrac   = 0.45 // more than this share near-white or near-black
	graphicMinColours = 90   // fewer distinct 4-bit-per-channel colours than this
)

type graphicStats struct {
	White, Dark float64
	Colours     int
}

func (s graphicStats) graphic() bool {
	return s.White > graphicFlatFrac || s.Dark > graphicFlatFrac || s.Colours < graphicMinColours
}

func measureGraphic(img image.Image) graphicStats {
	dst := image.NewRGBA(image.Rect(0, 0, graphicSide, graphicSide))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	var white, dark int
	colours := map[uint16]bool{}
	for i := 0; i < len(dst.Pix); i += 4 {
		r, g, b := dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2]
		if min(r, g, b) > graphicWhiteMin {
			white++
		}
		if max(r, g, b) < graphicDarkMax {
			dark++
		}
		colours[uint16(r>>4)<<8|uint16(g>>4)<<4|uint16(b>>4)] = true
	}
	n := float64(graphicSide * graphicSide)
	return graphicStats{White: float64(white) / n, Dark: float64(dark) / n, Colours: len(colours)}
}

// isGraphic decodes data and reports whether it is a card or screenshot rather
// than a photo. An image that does not decode is not judged a graphic.
func isGraphic(data []byte) (bool, graphicStats, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return false, graphicStats{}, err
	}
	s := measureGraphic(img)
	return s.graphic(), s, nil
}
