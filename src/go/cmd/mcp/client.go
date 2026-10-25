package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIClient is a minimal authenticated wrapper over the api-client gateway's
// /api/v2 REST surface. Responses are passed through as JSON rather than
// re-modelled: the MCP consumer (an LLM) reads the payloads directly, so the
// gateway's OpenAPI-documented shapes are the contract.
type APIClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewAPIClient returns a client for the gateway at baseURL that presents token
// as the static bearer on every request.
func NewAPIClient(baseURL, token string) *APIClient {
	return &APIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Get performs an authenticated GET against /api/v2 and returns the response
// body, pretty-printed when it is valid JSON.
func (c *APIClient) Get(ctx context.Context, path string, query url.Values) (string, error) {
	u := c.baseURL + "/api/v2" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("reading response from %s: %w", path, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "", fmt.Errorf("unauthorized (HTTP 401) — FITGLUE_API_TOKEN does not match the token api-client was started with")
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("not found (HTTP 404): %s", path)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", fmt.Errorf("%s returned HTTP %d: %s", path, resp.StatusCode, truncate(string(body), 500))
	}

	return prettyJSON(body), nil
}

// prettyJSON re-indents valid JSON and passes anything else through untouched.
func prettyJSON(body []byte) string {
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimSpace(body), "", "  "); err != nil {
		return string(body)
	}
	return out.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
