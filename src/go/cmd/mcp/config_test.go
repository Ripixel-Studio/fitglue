package main

import "testing"

func TestConfigValidate(t *testing.T) {
	if err := (Config{Token: "tok"}).Validate(); err != nil {
		t.Fatalf("Validate() with token: %v", err)
	}
	if err := (Config{}).Validate(); err == nil {
		t.Fatal("Validate() without token: want error")
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("FITGLUE_API_URL", "")
	t.Setenv("FITGLUE_API_TOKEN", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv() without FITGLUE_API_TOKEN: want error")
	}

	t.Setenv("FITGLUE_API_TOKEN", "tok")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if cfg.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL = %q, want default %q", cfg.BaseURL, defaultBaseURL)
	}
	if cfg.Token != "tok" {
		t.Errorf("Token = %q, want tok", cfg.Token)
	}

	t.Setenv("FITGLUE_API_URL", "http://127.0.0.1:8102")
	cfg, err = ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if cfg.BaseURL != "http://127.0.0.1:8102" {
		t.Errorf("BaseURL = %q, want override", cfg.BaseURL)
	}
}
