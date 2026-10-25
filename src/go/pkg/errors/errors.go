// Package errors provides structured error types for FitGlue.
//
// All errors in FitGlue should use these types to enable consistent
// error handling, logging, and retry logic across the codebase.
package errors

import (
	"fmt"
)

// ErrorCode represents a unique error identifier for categorization.
type ErrorCode string

// Common error codes used throughout FitGlue.
const (
	// User errors
	CodeUserNotFound     ErrorCode = "USER_NOT_FOUND"
	CodeUserUnauthorized ErrorCode = "USER_UNAUTHORIZED"
	CodeUserForbidden    ErrorCode = "USER_FORBIDDEN"

	// Integration errors
	CodeIntegrationNotFound    ErrorCode = "INTEGRATION_NOT_FOUND"
	CodeIntegrationExpired     ErrorCode = "INTEGRATION_EXPIRED"
	CodeIntegrationAuthFailed  ErrorCode = "INTEGRATION_AUTH_FAILED"
	CodeIntegrationRateLimited ErrorCode = "INTEGRATION_RATE_LIMITED"

	// Pipeline errors
	CodePipelineNotFound      ErrorCode = "PIPELINE_NOT_FOUND"
	CodePipelineInvalidConfig ErrorCode = "PIPELINE_INVALID_CONFIG"

	// Enricher errors
	CodeEnricherFailed   ErrorCode = "ENRICHER_FAILED"
	CodeEnricherNotFound ErrorCode = "ENRICHER_NOT_FOUND"
	CodeEnricherTimeout  ErrorCode = "ENRICHER_TIMEOUT"
	CodeEnricherSkipped  ErrorCode = "ENRICHER_SKIPPED"

	// Activity errors
	CodeActivityNotFound      ErrorCode = "ACTIVITY_NOT_FOUND"
	CodeActivityInvalidFormat ErrorCode = "ACTIVITY_INVALID_FORMAT"

	// Infrastructure errors
	CodeStorageError ErrorCode = "STORAGE_ERROR"
	CodePubSubError  ErrorCode = "PUBSUB_ERROR"
	CodeSecretError  ErrorCode = "SECRET_ERROR"

	// General errors
	CodeValidationError ErrorCode = "VALIDATION_ERROR"
	CodeInternalError   ErrorCode = "INTERNAL_ERROR"
	CodeTimeoutError    ErrorCode = "TIMEOUT_ERROR"
)

// FitGlueError is the base error type for all FitGlue errors.
// It provides structured error information including error codes,
// retry semantics, and contextual metadata.
type FitGlueError struct {
	Code      ErrorCode         // Unique error code for categorization
	Message   string            // Human-readable error message
	Cause     error             // Underlying error (if any)
	Retryable bool              // Whether the operation can be retried
	Metadata  map[string]string // Additional context
}

// Error implements the error interface.
func (e *FitGlueError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause for errors.Is/As support.
func (e *FitGlueError) Unwrap() error {
	return e.Cause
}

// WithCause wraps an underlying error.
func (e *FitGlueError) WithCause(cause error) *FitGlueError {
	return &FitGlueError{
		Code:      e.Code,
		Message:   e.Message,
		Cause:     cause,
		Retryable: e.Retryable,
		Metadata:  e.Metadata,
	}
}

// WithMessage adds a custom message.
func (e *FitGlueError) WithMessage(msg string) *FitGlueError {
	return &FitGlueError{
		Code:      e.Code,
		Message:   msg,
		Cause:     e.Cause,
		Retryable: e.Retryable,
		Metadata:  e.Metadata,
	}
}

// WithMetadata adds contextual metadata.
func (e *FitGlueError) WithMetadata(key, value string) *FitGlueError {
	meta := make(map[string]string)
	for k, v := range e.Metadata {
		meta[k] = v
	}
	meta[key] = value
	return &FitGlueError{
		Code:      e.Code,
		Message:   e.Message,
		Cause:     e.Cause,
		Retryable: e.Retryable,
		Metadata:  meta,
	}
}

// Pre-defined sentinel errors for common cases.
// Use these with errors.Is() or wrap them with .WithCause().
var (
	// User errors
	ErrUserNotFound     = &FitGlueError{Code: CodeUserNotFound, Message: "user not found", Retryable: false}
	ErrUserUnauthorized = &FitGlueError{Code: CodeUserUnauthorized, Message: "unauthorized", Retryable: false}
	ErrUserForbidden    = &FitGlueError{Code: CodeUserForbidden, Message: "forbidden", Retryable: false}

	// Integration errors
	ErrIntegrationNotFound    = &FitGlueError{Code: CodeIntegrationNotFound, Message: "integration not found", Retryable: false}
	ErrIntegrationExpired     = &FitGlueError{Code: CodeIntegrationExpired, Message: "integration token expired", Retryable: true}
	ErrIntegrationAuthFailed  = &FitGlueError{Code: CodeIntegrationAuthFailed, Message: "integration authentication failed", Retryable: false}
	ErrIntegrationRateLimited = &FitGlueError{Code: CodeIntegrationRateLimited, Message: "integration rate limited", Retryable: true}

	// Pipeline errors
	ErrPipelineNotFound      = &FitGlueError{Code: CodePipelineNotFound, Message: "pipeline not found", Retryable: false}
	ErrPipelineInvalidConfig = &FitGlueError{Code: CodePipelineInvalidConfig, Message: "invalid pipeline configuration", Retryable: false}

	// Enricher errors
	ErrEnricherFailed   = &FitGlueError{Code: CodeEnricherFailed, Message: "enricher failed", Retryable: true}
	ErrEnricherNotFound = &FitGlueError{Code: CodeEnricherNotFound, Message: "enricher not found", Retryable: false}
	ErrEnricherTimeout  = &FitGlueError{Code: CodeEnricherTimeout, Message: "enricher timed out", Retryable: true}
	ErrEnricherSkipped  = &FitGlueError{Code: CodeEnricherSkipped, Message: "enricher skipped", Retryable: false}

	// Activity errors
	ErrActivityNotFound      = &FitGlueError{Code: CodeActivityNotFound, Message: "activity not found", Retryable: false}
	ErrActivityInvalidFormat = &FitGlueError{Code: CodeActivityInvalidFormat, Message: "invalid activity format", Retryable: false}

	// Infrastructure errors
	ErrStorageError = &FitGlueError{Code: CodeStorageError, Message: "storage error", Retryable: true}
	ErrPubSubError  = &FitGlueError{Code: CodePubSubError, Message: "pubsub error", Retryable: true}
	ErrSecretError  = &FitGlueError{Code: CodeSecretError, Message: "secret access error", Retryable: true}

	// General errors
	ErrValidation = &FitGlueError{Code: CodeValidationError, Message: "validation error", Retryable: false}
	ErrInternal   = &FitGlueError{Code: CodeInternalError, Message: "internal error", Retryable: false}
	ErrTimeout    = &FitGlueError{Code: CodeTimeoutError, Message: "timeout", Retryable: true}
)

// New creates a new FitGlueError with the given code and message.
func New(code ErrorCode, message string) *FitGlueError {
	return &FitGlueError{
		Code:      code,
		Message:   message,
		Retryable: false,
	}
}

// NewRetryable creates a new retryable FitGlueError.
func NewRetryable(code ErrorCode, message string) *FitGlueError {
	return &FitGlueError{
		Code:      code,
		Message:   message,
		Retryable: true,
	}
}

// Wrap wraps an error with a FitGlueError.
func Wrap(cause error, code ErrorCode, message string) *FitGlueError {
	return &FitGlueError{
		Code:      code,
		Message:   message,
		Cause:     cause,
		Retryable: false,
	}
}

// WrapRetryable wraps an error with a retryable FitGlueError.
func WrapRetryable(cause error, code ErrorCode, message string) *FitGlueError {
	return &FitGlueError{
		Code:      code,
		Message:   message,
		Cause:     cause,
		Retryable: true,
	}
}

// IsRetryable checks if an error is retryable.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if fgErr, ok := err.(*FitGlueError); ok {
		return fgErr.Retryable
	}
	return false
}

// GetCode extracts the error code from an error, if available.
func GetCode(err error) ErrorCode {
	if err == nil {
		return ""
	}
	if fgErr, ok := err.(*FitGlueError); ok {
		return fgErr.Code
	}
	return CodeInternalError
}
