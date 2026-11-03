package route_thumbnail

// RouteSVGTemplate is the SVG template for the route visualization
// Uses FitGlue's signature color palette
const RouteSVGTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {{.Width}} {{.Height}}" width="{{.Width}}" height="{{.Height}}">
  <defs>
    <linearGradient id="routeGradient" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" style="stop-color:#FF1B8D;stop-opacity:1" />
      <stop offset="50%" style="stop-color:#7C3AED;stop-opacity:1" />
      <stop offset="100%" style="stop-color:#06FFA5;stop-opacity:1" />
    </linearGradient>
    <filter id="glow">
      <feGaussianBlur stdDeviation="3" result="coloredBlur"/>
      <feMerge>
        <feMergeNode in="coloredBlur"/>
        <feMergeNode in="SourceGraphic"/>
      </feMerge>
    </filter>
  </defs>

  <!-- Background -->
  <rect width="{{.Width}}" height="{{.Height}}" fill="#1a1a2e"/>

  <!-- Route Path -->
  <path d="{{.PathData}}" fill="none" stroke="url(#routeGradient)" stroke-width="4" stroke-linecap="round" stroke-linejoin="round" filter="url(#glow)"/>

  <!-- Start Point -->
  <circle cx="{{.StartX}}" cy="{{.StartY}}" r="8" fill="#06FFA5">
    <animate attributeName="r" values="8;10;8" dur="2s" repeatCount="indefinite"/>
  </circle>
  <circle cx="{{.StartX}}" cy="{{.StartY}}" r="4" fill="#fff"/>

  <!-- End Point -->
  <circle cx="{{.EndX}}" cy="{{.EndY}}" r="8" fill="#FF1B8D">
    <animate attributeName="r" values="8;10;8" dur="2s" repeatCount="indefinite"/>
  </circle>
  <circle cx="{{.EndX}}" cy="{{.EndY}}" r="4" fill="#fff"/>

  <!-- Labels -->
  <g font-family="sans-serif" font-size="10" fill="#fff">
    <text x="{{.StartX}}" y="{{.StartY}}" dy="-15" text-anchor="middle">START</text>
    <text x="{{.EndX}}" y="{{.EndY}}" dy="20" text-anchor="middle">FINISH</text>
  </g>
</svg>`

// PlaceholderSVG stands in for a route that is withheld (inside a privacy zone, or no
// zones available): same background, no route.
const PlaceholderSVG = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400" width="400" height="400">
  <rect width="400" height="400" fill="#1a1a2e"/>
  <circle cx="200" cy="200" r="48" fill="none" stroke="#7C3AED" stroke-width="4" stroke-dasharray="6 10" stroke-linecap="round"/>
</svg>
`
