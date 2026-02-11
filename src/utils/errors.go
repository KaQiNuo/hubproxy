package utils

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type AppError struct {
	Code        string                 `json:"code"`
	Message     string                 `json:"message"`
	Details     map[string]string     `json:"details,omitempty"`
	StatusCode  int                    `json:"-"`
	InternalErr error                  `json:"-"`
	Timestamp   time.Time             `json:"timestamp"`
	RequestID   string                `json:"request_id,omitempty"`
	Path        string                `json:"path,omitempty"`
	Method      string                `json:"method,omitempty"`
	StackTrace  string                `json:"-"`
}

var (
	ErrDockerHubUnavailable = &AppError{
		Code:       "DOCKER_HUB_UNAVAILABLE",
		Message:    "Docker Hub服务暂时不可用",
		StatusCode: 503,
	}

	ErrRegistryNotSupported = &AppError{
		Code:       "REGISTRY_NOT_SUPPORTED",
		Message:    "不支持的镜像仓库",
		StatusCode: 400,
	}

	ErrRateLimitExceeded = &AppError{
		Code:       "RATE_LIMIT_EXCEEDED",
		Message:    "请求频率超限，请稍后重试",
		StatusCode: 429,
	}

	ErrAccessDenied = &AppError{
		Code:       "ACCESS_DENIED",
		Message:    "访问被拒绝",
		StatusCode: 403,
	}

	ErrImageNotFound = &AppError{
		Code:       "IMAGE_NOT_FOUND",
		Message:    "镜像不存在",
		StatusCode: 404,
	}

	ErrInvalidRequest = &AppError{
		Code:       "INVALID_REQUEST",
		Message:    "无效的请求参数",
		StatusCode: 400,
	}

	ErrTokenExpired = &AppError{
		Code:       "TOKEN_EXPIRED",
		Message:    "认证令牌已过期",
		StatusCode: 401,
	}

	ErrInternalError = &AppError{
		Code:       "INTERNAL_ERROR",
		Message:    "内部服务器错误",
		StatusCode: 500,
	}

	ErrSearchFailed = &AppError{
		Code:       "SEARCH_FAILED",
		Message:    "搜索服务暂时不可用",
		StatusCode: 503,
	}

	ErrTimeout = &AppError{
		Code:       "TIMEOUT",
		Message:    "请求超时",
		StatusCode: 504,
	}
)

func (e *AppError) Error() string {
	if e.InternalErr != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.InternalErr)
	}
	return e.Message
}

func (e *AppError) WithDetails(details map[string]string) *AppError {
	newErr := *e
	newErr.Details = details
	return &newErr
}

func (e *AppError) WithInternalErr(err error) *AppError {
	newErr := *e
	newErr.InternalErr = err
	newErr.StackTrace = getStackTrace(3)
	return &newErr
}

func (e *AppError) WithRequest(requestID, path, method string) *AppError {
	newErr := *e
	newErr.RequestID = requestID
	newErr.Path = path
	newErr.Method = method
	return &newErr
}

func NewAppError(code, message string, statusCode int) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		StatusCode: statusCode,
		Timestamp:  time.Now(),
	}
}

func WrapError(err error, code, message string, statusCode int) *AppError {
	if appErr, ok := err.(*AppError); ok {
		return appErr
	}

	return &AppError{
		Code:        code,
		Message:     message,
		InternalErr: err,
		StatusCode:  statusCode,
		Timestamp:   time.Now(),
		StackTrace:  getStackTrace(3),
	}
}

func getStackTrace(skip int) string {
	buf := make([]byte, 2048)
	n := runtime.Stack(buf, true)
	lines := strings.Split(string(buf[:n]), "\n")

	if skip*2 < len(lines) {
		return strings.Join(lines[skip*2:min(skip*2+10, len(lines))], "\n")
	}
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type ErrorHandler struct {
	logger        *Logger
	showStackTrace bool
}

func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{
		logger:         GetLogger(),
		showStackTrace: false,
	}
}

func (h *ErrorHandler) SetShowStackTrace(show bool) {
	h.showStackTrace = show
}

func (h *ErrorHandler) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) > 0 {
			err := c.Errors.Last().Err

			var appErr *AppError
			if errors, ok := err.(*AppError); ok {
				appErr = errors
			} else {
				appErr = WrapError(err, "INTERNAL_ERROR", "内部错误", 500)
			}

			requestID := c.GetHeader("X-Request-ID")
			if requestID == "" {
				requestID = c.GetHeader("X-Correlation-ID")
			}

			logEntry := h.logger.buildEntry(ERROR, appErr.Message, nil)
			logEntry.RequestID = requestID
			logEntry.Path = c.Request.URL.Path
			logEntry.Method = c.Request.Method
			logEntry.Error = appErr.Error()
			logEntry.StackTrace = appErr.StackTrace

			if h.showStackTrace && appErr.StackTrace != "" {
				logEntry.Message = fmt.Sprintf("%s\nStack: %s", appErr.Message, appErr.StackTrace)
			}

			if appErr.InternalErr != nil {
				logEntry.Fields["internal_error"] = appErr.InternalErr.Error()
			}

			c.JSON(appErr.StatusCode, gin.H{
				"error": gin.H{
					"code":     appErr.Code,
					"message":  appErr.Message,
					"details":  appErr.Details,
					"request": gin.H{
						"id":     requestID,
						"path":   c.Request.URL.Path,
						"method": c.Request.Method,
					},
					"timestamp": appErr.Timestamp.Format(time.RFC3339),
				},
			})
		}
	}
}

func (h *ErrorHandler) HandleError(c *gin.Context, err error) {
	var appErr *AppError

	switch e := err.(type) {
	case *AppError:
		appErr = e
	case error:
		appErr = WrapError(e, "INTERNAL_ERROR", "处理请求时发生错误", 500)
	default:
		appErr = NewAppError("UNKNOWN_ERROR", "未知错误", 500)
	}

	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = c.GetHeader("X-Correlation-ID")
	}

	logEntry := h.logger.buildEntry(ERROR, appErr.Message, nil)
	logEntry.RequestID = requestID
	logEntry.Path = c.Request.URL.Path
	logEntry.Method = c.Request.Method
	logEntry.Error = appErr.Error()

	if appErr.InternalErr != nil {
		logEntry.Fields["internal_error"] = appErr.InternalErr.Error()
		logEntry.Fields["stack_trace"] = appErr.StackTrace
	}

	c.JSON(appErr.StatusCode, gin.H{
		"error": gin.H{
			"code":     appErr.Code,
			"message":  appErr.Message,
			"details":  appErr.Details,
			"request": gin.H{
				"id":     requestID,
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
			},
			"timestamp": appErr.Timestamp.Format(time.RFC3339),
		},
	})
}

func (h *ErrorHandler) HandleNotFound(c *gin.Context) {
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = c.GetHeader("X-Correlation-ID")
	}

	err := NewAppError("NOT_FOUND", "请求的资源不存在", 404).
		WithRequest(requestID, c.Request.URL.Path, c.Request.Method)

	c.JSON(err.StatusCode, gin.H{
		"error": gin.H{
			"code":     err.Code,
			"message":  err.Message,
			"details":  err.Details,
			"request": gin.H{
				"id":     requestID,
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
			},
			"timestamp": err.Timestamp.Format(time.RFC3339),
		},
	})
}

func (h *ErrorHandler) HandleBadRequest(c *gin.Context, message string, details map[string]string) {
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = c.GetHeader("X-Correlation-ID")
	}

	err := NewAppError("BAD_REQUEST", message, 400).
		WithDetails(details).
		WithRequest(requestID, c.Request.URL.Path, c.Request.Method)

	c.JSON(err.StatusCode, gin.H{
		"error": gin.H{
			"code":     err.Code,
			"message":  err.Message,
			"details":  err.Details,
			"request": gin.H{
				"id":     requestID,
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
			},
			"timestamp": err.Timestamp.Format(time.RFC3339),
		},
	})
}

func (h *ErrorHandler) HandleUnauthorized(c *gin.Context, message string) {
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = c.GetHeader("X-Correlation-ID")
	}

	err := NewAppError("UNAUTHORIZED", message, 401).
		WithRequest(requestID, c.Request.URL.Path, c.Request.Method)

	c.JSON(err.StatusCode, gin.H{
		"error": gin.H{
			"code":     err.Code,
			"message":  err.Message,
			"details":  err.Details,
			"request": gin.H{
				"id":     requestID,
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
			},
			"timestamp": err.Timestamp.Format(time.RFC3339),
		},
	})
}

func (h *ErrorHandler) HandleTooManyRequests(c *gin.Context, retryAfter time.Duration) {
	requestID := c.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = c.GetHeader("X-Correlation-ID")
	}

	err := NewAppError("RATE_LIMIT_EXCEEDED", "请求频率超限，请稍后重试", 429).
		WithRequest(requestID, c.Request.URL.Path, c.Request.Method)

	c.Header("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())))

	c.JSON(err.StatusCode, gin.H{
		"error": gin.H{
			"code":     err.Code,
			"message":  err.Message,
			"details": gin.H{
				"retry_after_seconds": int(retryAfter.Seconds()),
			},
			"request": gin.H{
				"id":     requestID,
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
			},
			"timestamp": err.Timestamp.Format(time.RFC3339),
		},
	})
}

func IsClientError(statusCode int) bool {
	return statusCode >= 400 && statusCode < 500
}

func IsServerError(statusCode int) bool {
	return statusCode >= 500
}

func IsSuccess(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}

func IsRedirect(statusCode int) bool {
	return statusCode >= 300 && statusCode < 400
}

func MapHTTPError(statusCode int) *AppError {
	switch statusCode {
	case http.StatusBadRequest:
		return ErrInvalidRequest
	case http.StatusUnauthorized:
		return ErrTokenExpired
	case http.StatusForbidden:
		return ErrAccessDenied
	case http.StatusNotFound:
		return ErrImageNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimitExceeded
	case http.StatusGatewayTimeout:
		return ErrTimeout
	case http.StatusServiceUnavailable:
		return ErrDockerHubUnavailable
	default:
		if IsServerError(statusCode) {
			return ErrInternalError
		}
	}
	return nil
}
