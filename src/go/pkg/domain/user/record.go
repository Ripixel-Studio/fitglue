package user

import (
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

// Record wraps a UserProfile with its associated decoupled data.
// This allows cleanly separated protobuf definitions while providing a unified
// interface for Go consumers (e.g., user.Integrations.Strava.AccessToken).
type Record struct {
	*pbuser.UserProfile
	Integrations *pbuser.UserIntegrations
}
