package utils

import (
	"errors"
	"testing"
)

func TestAppErrorCreation(t *testing.T) {
	err := NewAppError("TEST_CODE", "test message", 400)

	if err.Code != "TEST_CODE" {
		t.Errorf("Expected code 'TEST_CODE', got '%s'", err.Code)
	}

	if err.Message != "test message" {
		t.Errorf("Expected message 'test message', got '%s'", err.Message)
	}

	if err.StatusCode != 400 {
		t.Errorf("Expected status code 400, got %d", err.StatusCode)
	}

	if err.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
}

func TestAppErrorWithDetails(t *testing.T) {
	err := NewAppError("TEST_CODE", "test message", 400)

	details := map[string]string{
		"field": "value",
	}

	errWithDetails := err.WithDetails(details)

	if errWithDetails.Details["field"] != "value" {
		t.Error("Details should be preserved")
	}
}

func TestAppErrorWithInternalErr(t *testing.T) {
	internalErr := errors.New("internal error")

	err := NewAppError("TEST_CODE", "test message", 500)
	errWithInternal := err.WithInternalErr(internalErr)

	if errWithInternal.InternalErr != internalErr {
		t.Error("Internal error should be preserved")
	}

	if errWithInternal.StackTrace == "" {
		t.Error("Stack trace should be generated")
	}
}

func TestWrapError(t *testing.T) {
	internalErr := errors.New("database connection failed")

	wrapped := WrapError(internalErr, "DB_ERROR", "Database error", 500)

	if wrapped.Code != "DB_ERROR" {
		t.Errorf("Expected code 'DB_ERROR', got '%s'", wrapped.Code)
	}

	if wrapped.InternalErr != internalErr {
		t.Error("Internal error should be preserved")
	}

	if wrapped.StackTrace == "" {
		t.Error("Stack trace should be generated")
	}
}

func TestWrapErrorWithAppError(t *testing.T) {
	original := NewAppError("ORIGINAL", "original message", 400)

	wrapped := WrapError(original, "WRAPPED", "wrapped message", 500)

	if wrapped.Code != "ORIGINAL" {
		t.Errorf("Expected code 'ORIGINAL', got '%s'", wrapped.Code)
	}
}

func TestAppErrorError(t *testing.T) {
	err := NewAppError("TEST", "test message", 400)

	if err.Error() != "test message" {
		t.Errorf("Expected 'test message', got '%s'", err.Error())
	}

	internalErr := errors.New("internal")
	errWithInternal := NewAppError("TEST", "test message", 400).WithInternalErr(internalErr)

	if errWithInternal.Error() != "test message: internal" {
		t.Errorf("Expected 'test message: internal', got '%s'", errWithInternal.Error())
	}
}

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		err         *AppError
		expectedCode string
		expectedMsg  string
		expectedSC  int
	}{
		{ErrDockerHubUnavailable, "DOCKER_HUB_UNAVAILABLE", "Docker Hub服务暂时不可用", 503},
		{ErrRegistryNotSupported, "REGISTRY_NOT_SUPPORTED", "不支持的镜像仓库", 400},
		{ErrRateLimitExceeded, "RATE_LIMIT_EXCEEDED", "请求频率超限，请稍后重试", 429},
		{ErrAccessDenied, "ACCESS_DENIED", "访问被拒绝", 403},
		{ErrImageNotFound, "IMAGE_NOT_FOUND", "镜像不存在", 404},
		{ErrInvalidRequest, "INVALID_REQUEST", "无效的请求参数", 400},
		{ErrTokenExpired, "TOKEN_EXPIRED", "认证令牌已过期", 401},
		{ErrInternalError, "INTERNAL_ERROR", "内部服务器错误", 500},
		{ErrSearchFailed, "SEARCH_FAILED", "搜索服务暂时不可用", 503},
		{ErrTimeout, "TIMEOUT", "请求超时", 504},
	}

	for _, tt := range tests {
		if tt.err.Code != tt.expectedCode {
			t.Errorf("Expected code '%s', got '%s'", tt.expectedCode, tt.err.Code)
		}

		if tt.err.Message != tt.expectedMsg {
			t.Errorf("Expected message '%s', got '%s'", tt.expectedMsg, tt.err.Message)
		}

		if tt.err.StatusCode != tt.expectedSC {
			t.Errorf("Expected status code %d, got %d", tt.expectedSC, tt.err.StatusCode)
		}
	}
}

func TestErrorHandlerCreation(t *testing.T) {
	handler := NewErrorHandler()

	if handler == nil {
		t.Fatal("Error handler should not be nil")
	}

	if handler.logger == nil {
		t.Fatal("Logger should be initialized")
	}
}

func TestErrorHandlerShowStackTrace(t *testing.T) {
	handler := NewErrorHandler()

	handler.SetShowStackTrace(true)

	handler.SetShowStackTrace(false)
}

func TestMapHTTPError(t *testing.T) {
	tests := []struct {
		statusCode   int
		expectedCode string
	}{
		{400, "INVALID_REQUEST"},
		{401, "TOKEN_EXPIRED"},
		{403, "ACCESS_DENIED"},
		{404, "IMAGE_NOT_FOUND"},
		{429, "RATE_LIMIT_EXCEEDED"},
		{504, "TIMEOUT"},
		{503, "DOCKER_HUB_UNAVAILABLE"},
		{200, ""},
		{300, ""},
	}

	for _, tt := range tests {
		err := MapHTTPError(tt.statusCode)

		if tt.expectedCode == "" {
			if err != nil {
				t.Errorf("Expected nil error for status %d, got '%s'", tt.statusCode, err.Code)
			}
		} else {
			if err == nil {
				t.Errorf("Expected error for status %d, got nil", tt.statusCode)
			} else if err.Code != tt.expectedCode {
				t.Errorf("Expected code '%s' for status %d, got '%s'", tt.expectedCode, tt.statusCode, err.Code)
			}
		}
	}
}

func TestIsClientError(t *testing.T) {
	if !IsClientError(400) {
		t.Error("400 should be client error")
	}

	if !IsClientError(499) {
		t.Error("499 should be client error")
	}

	if IsClientError(500) {
		t.Error("500 should not be client error")
	}

	if IsClientError(200) {
		t.Error("200 should not be client error")
	}
}

func TestIsServerError(t *testing.T) {
	if !IsServerError(500) {
		t.Error("500 should be server error")
	}

	if !IsServerError(599) {
		t.Error("599 should be server error")
	}

	if IsServerError(400) {
		t.Error("400 should not be server error")
	}
}

func TestIsSuccess(t *testing.T) {
	if !IsSuccess(200) {
		t.Error("200 should be success")
	}

	if !IsSuccess(201) {
		t.Error("201 should be success")
	}

	if IsSuccess(400) {
		t.Error("400 should not be success")
	}
}

func TestIsRedirect(t *testing.T) {
	if !IsRedirect(301) {
		t.Error("301 should be redirect")
	}

	if !IsRedirect(302) {
		t.Error("302 should be redirect")
	}

	if IsRedirect(200) {
		t.Error("200 should not be redirect")
	}
}

func TestMinFunction(t *testing.T) {
	if min(1, 2) != 1 {
		t.Error("min(1, 2) should return 1")
	}

	if min(5, 3) != 3 {
		t.Error("min(5, 3) should return 3")
	}

	if min(-1, 0) != -1 {
		t.Error("min(-1, 0) should return -1")
	}
}

func TestAppErrorWithRequest(t *testing.T) {
	err := NewAppError("TEST", "test message", 400)

	errWithRequest := err.WithRequest("req-123", "/test", "GET")

	if errWithRequest.RequestID != "req-123" {
		t.Errorf("Expected request ID 'req-123', got '%s'", errWithRequest.RequestID)
	}

	if errWithRequest.Path != "/test" {
		t.Errorf("Expected path '/test', got '%s'", errWithRequest.Path)
	}

	if errWithRequest.Method != "GET" {
		t.Errorf("Expected method 'GET', got '%s'", errWithRequest.Method)
	}
}

func BenchmarkAppErrorCreation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NewAppError("TEST", "test message", 400)
	}
}

func BenchmarkWrapError(b *testing.B) {
	internalErr := errors.New("internal error")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		WrapError(internalErr, "CODE", "message", 500)
	}
}

func BenchmarkMapHTTPError(b *testing.B) {
	statusCodes := []int{200, 400, 401, 403, 404, 429, 500, 503, 504}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MapHTTPError(statusCodes[i%len(statusCodes)])
	}
}

func BenchmarkIsClientError(b *testing.B) {
	statusCodes := []int{200, 300, 400, 404, 499, 500}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IsClientError(statusCodes[i%len(statusCodes)])
	}
}

func BenchmarkIsServerError(b *testing.B) {
	statusCodes := []int{200, 400, 500, 503}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IsServerError(statusCodes[i%len(statusCodes)])
	}
}
