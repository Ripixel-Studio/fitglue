package server

import "os"

type OAuthProviderConfig struct {
	AuthURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

func GetOAuthConfig(provider string) *OAuthProviderConfig {
	switch provider {
	case "strava":
		return &OAuthProviderConfig{
			AuthURL:      "https://www.strava.com/oauth/authorize",
			TokenURL:     "https://www.strava.com/oauth/token",
			ClientID:     os.Getenv("STRAVA_CLIENT_ID"),
			ClientSecret: os.Getenv("STRAVA_CLIENT_SECRET"),
			Scopes:       []string{"read_all", "activity:read_all", "activity:write"},
		}
	case "fitbit":
		return &OAuthProviderConfig{
			AuthURL:      "https://www.fitbit.com/oauth2/authorize",
			TokenURL:     "https://api.fitbit.com/oauth2/token",
			ClientID:     os.Getenv("FITBIT_CLIENT_ID"),
			ClientSecret: os.Getenv("FITBIT_CLIENT_SECRET"),
			Scopes:       []string{"activity", "profile", "heartrate"},
		}
	case "spotify":
		return &OAuthProviderConfig{
			AuthURL:      "https://accounts.spotify.com/authorize",
			TokenURL:     "https://accounts.spotify.com/api/token",
			ClientID:     os.Getenv("SPOTIFY_CLIENT_ID"),
			ClientSecret: os.Getenv("SPOTIFY_CLIENT_SECRET"),
			Scopes:       []string{"user-read-recently-played", "user-read-playback-state"},
		}
	case "github":
		return &OAuthProviderConfig{
			AuthURL:      "https://github.com/login/oauth/authorize",
			TokenURL:     "https://github.com/login/oauth/access_token",
			ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
			ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
			Scopes:       []string{"read:user"},
		}
	}
	return nil
}
