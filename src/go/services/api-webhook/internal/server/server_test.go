package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWebhookWriteError_gRPCCode_NotFound(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, status.Error(codes.NotFound, "not found"))
	assert.Equal(t, http.StatusNotFound, w.Code)

	var apiErr APIError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&apiErr))
	assert.Equal(t, "NOT_FOUND", apiErr.Code)
	assert.Equal(t, "not found", apiErr.Message)
}

func TestWebhookWriteError_AllGRPCCodes(t *testing.T) {
	tests := []struct {
		code     codes.Code
		httpCode int
		errCode  string
	}{
		{codes.InvalidArgument, http.StatusBadRequest, "INVALID_ARGUMENT"},
		{codes.PermissionDenied, http.StatusForbidden, "PERMISSION_DENIED"},
		{codes.Unauthenticated, http.StatusUnauthorized, "UNAUTHENTICATED"},
		{codes.AlreadyExists, http.StatusConflict, "ALREADY_EXISTS"},
		{codes.Unimplemented, http.StatusNotImplemented, "NOT_IMPLEMENTED"},
		{codes.Unavailable, http.StatusServiceUnavailable, "UNAVAILABLE"},
		{codes.FailedPrecondition, http.StatusPreconditionFailed, "FAILED_PRECONDITION"},
		{codes.ResourceExhausted, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"},
		{codes.DeadlineExceeded, http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"},
		{codes.Internal, http.StatusInternalServerError, "INTERNAL_ERROR"},
	}

	for _, tc := range tests {
		t.Run(tc.errCode, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteError(w, status.Error(tc.code, "msg"))
			assert.Equal(t, tc.httpCode, w.Code)

			var apiErr APIError
			require.NoError(t, json.NewDecoder(w.Body).Decode(&apiErr))
			assert.Equal(t, tc.errCode, apiErr.Code)
		})
	}
}

func TestWebhookWriteError_CustomError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, &CustomError{HTTPCode: http.StatusTeapot, Msg: "teapot"})
	assert.Equal(t, http.StatusTeapot, w.Code)

	var apiErr APIError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&apiErr))
	assert.Equal(t, "CLIENT_ERROR", apiErr.Code)
	assert.Equal(t, "teapot", apiErr.Message)
}

func TestRawBodyMiddleware(t *testing.T) {
	body := []byte(`{"event":"test"}`)
	called := false
	var capturedBody []byte

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// RawBodyMiddleware restores r.Body so next handler can re-read it
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	handler := RawBodyMiddleware(next)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, capturedBody)
}

func TestJSONResponseMiddleware_Webhook(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := JSONResponseMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusOK, w.Code)
}
