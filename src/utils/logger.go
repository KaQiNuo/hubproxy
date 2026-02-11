package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

func (level LogLevel) String() string {
	switch level {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

type LogEntry struct {
	Timestamp   time.Time              `json:"timestamp"`
	Level       string                 `json:"level"`
	Message     string                 `json:"message"`
	Service     string                 `json:"service"`
	Version     string                 `json:"version"`
	RequestID   string                 `json:"request_id,omitempty"`
	ClientIP    string                 `json:"client_ip,omitempty"`
	UserAgent   string                 `json:"user_agent,omitempty"`
	Method      string                 `json:"method,omitempty"`
	Path        string                 `json:"path,omitempty"`
	StatusCode  int                    `json:"status_code,omitempty"`
	Duration    float64                `json:"duration_seconds,omitempty"`
	Error       string                 `json:"error,omitempty"`
	StackTrace  string                 `json:"stack_trace,omitempty"`
	Fields      map[string]interface{} `json:"fields,omitempty"`
	GoroutineID int                    `json:"goroutine_id"`
	Caller      string                 `json:"caller,omitempty"`
}

type Logger struct {
	mu         sync.RWMutex
	level      LogLevel
	output     io.Writer
	service    string
	version    string
	requestID  string
	clientIP   string
	userAgent  string
	enableJSON bool
	enableFile bool
	logDir     string
}

var (
	defaultLogger *Logger
	loggerMu      sync.Once
)

func InitLogger(serviceName, version string) {
	loggerMu.Do(func() {
		logDir := os.Getenv("LOG_DIR")
		if logDir == "" {
			logDir = "logs"
		}

		defaultLogger = &Logger{
			level:      INFO,
			output:     os.Stdout,
			service:    serviceName,
			version:    version,
			enableJSON: true,
			enableFile: true,
			logDir:     logDir,
		}

		if err := os.MkdirAll(logDir, 0755); err != nil {
			fmt.Printf("Warning: Failed to create log directory: %v\n", err)
			defaultLogger.enableFile = false
		}
	})
}

func GetLogger() *Logger {
	if defaultLogger == nil {
		loggerMu.Do(func() {
			defaultLogger = &Logger{
				level:      INFO,
				output:     os.Stdout,
				service:    "hubproxy",
				version:    "dev",
				enableJSON: true,
				enableFile: false,
			}
		})
	}
	return defaultLogger
}

func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

func (l *Logger) SetOutput(writer io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = writer
}

func (l *Logger) WithRequestID(requestID string) *Logger {
	newLogger := *l
	newLogger.requestID = requestID
	return &newLogger
}

func (l *Logger) WithClientIP(clientIP string) *Logger {
	newLogger := *l
	newLogger.clientIP = clientIP
	return &newLogger
}

func (l *Logger) WithUserAgent(userAgent string) *Logger {
	newLogger := *l
	newLogger.userAgent = userAgent
	return &newLogger
}

func (l *Logger) Debug(message string, fields ...interface{}) {
	l.log(DEBUG, message, fields...)
}

func (l *Logger) Info(message string, fields ...interface{}) {
	l.log(INFO, message, fields...)
}

func (l *Logger) Warn(message string, fields ...interface{}) {
	l.log(WARN, message, fields...)
}

func (l *Logger) Error(message string, fields ...interface{}) {
	l.log(ERROR, message, fields...)
}

func (l *Logger) Fatal(message string, fields ...interface{}) {
	l.log(FATAL, message, fields...)
}

func (l *Logger) log(level LogLevel, message string, fields ...interface{}) {
	l.mu.RLock()
	if level < l.level {
		l.mu.RUnlock()
		return
	}
	l.mu.RUnlock()

	entry := l.buildEntry(level, message, fields...)

	if l.enableJSON {
		l.writeJSON(entry)
	} else {
		l.writePlain(entry)
	}

	if l.enableFile {
		l.writeToFile(entry)
	}

	if level == FATAL {
		os.Exit(1)
	}
}

func (l *Logger) buildEntry(level LogLevel, message string, fields ...interface{}) *LogEntry {
	entry := &LogEntry{
		Timestamp:  time.Now(),
		Level:      level.String(),
		Message:    message,
		Service:    l.service,
		Version:    l.version,
		RequestID:  l.requestID,
		ClientIP:   l.clientIP,
		UserAgent:  l.userAgent,
		GoroutineID: getGoroutineID(),
	}

	if len(fields) > 0 {
		entry.Fields = make(map[string]interface{})
		for i := 0; i < len(fields)-1; i += 2 {
			key, ok := fields[i].(string)
			if !ok {
				continue
			}
			entry.Fields[key] = fields[i+1]
		}
	}

	if level >= ERROR {
		entry.Caller = getCaller(3)
	}

	return entry
}

func (l *Logger) writeJSON(entry *LogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Failed to marshal log entry: %v\n", err)
		return
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	fmt.Fprintln(l.output, string(data))
}

func (l *Logger) writePlain(entry *LogEntry) {
	fieldsStr := ""
	if len(entry.Fields) > 0 {
		fieldList := make([]string, 0, len(entry.Fields))
		for key, value := range entry.Fields {
			fieldList = append(fieldList, fmt.Sprintf("%s=%v", key, value))
		}
		fieldsStr = " " + joinStrings(fieldList, " ")
	}

	output := fmt.Sprintf("[%s] %s | %s | %s%s",
		entry.Timestamp.Format("2006-01-02 15:04:05"),
		entry.Level,
		entry.Message,
		entry.Service,
		fieldsStr,
	)

	if entry.Error != "" {
		output += fmt.Sprintf(" | error=%s", entry.Error)
	}
	if entry.Caller != "" {
		output += fmt.Sprintf(" | caller=%s", entry.Caller)
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	fmt.Fprintln(l.output, output)
}

func (l *Logger) writeToFile(entry *LogEntry) {
	if !l.enableFile || l.logDir == "" {
		return
	}

	filename := filepath.Join(l.logDir, fmt.Sprintf("%s.log", time.Now().Format("2006-01-02")))
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	fmt.Fprintln(file, string(data))
}

func (l *Logger) LogRequest(c *gin.Context, duration time.Duration, err error) {
	statusCode := c.Writer.Status()
	method := c.Request.Method
	path := c.Request.URL.Path

	level := INFO
	if statusCode >= 400 {
		level = WARN
	}
	if statusCode >= 500 {
		level = ERROR
	}

	entry := l.buildEntry(level, "HTTP request completed", nil)
	entry.Method = method
	entry.Path = path
	entry.StatusCode = statusCode
	entry.Duration = duration.Seconds()
	entry.RequestID = l.getRequestID(c)
	entry.ClientIP = c.ClientIP()
	entry.UserAgent = c.Request.UserAgent()

	if err != nil {
		entry.Error = err.Error()
		entry.StackTrace = getStackTrace(3)
	}

	if l.enableJSON {
		l.writeJSON(entry)
	} else {
		l.writePlain(entry)
	}

	if l.enableFile {
		l.writeToFile(entry)
	}
}

func (l *Logger) getRequestID(c *gin.Context) string {
	if id := c.GetHeader("X-Request-ID"); id != "" {
		return id
	}
	if id := c.GetHeader("X-Correlation-ID"); id != "" {
		return id
	}
	return ""
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

func getGoroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	idField := strings.Fields(string(buf[:n]))[1]
	var id int
	fmt.Sscanf(idField, "%d", &id)
	return id
}

func getCaller(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return ""
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return ""
	}
	return fn.Name()
}

func LogHTTPRequest(logger *Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start)
		logger.LogRequest(c, duration, nil)
	}
}

type RequestLogger struct {
	logger *Logger
}

func NewRequestLogger(serviceName, version string) *RequestLogger {
	InitLogger(serviceName, version)
	return &RequestLogger{
		logger: GetLogger(),
	}
}

func (rl *RequestLogger) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start)
		rl.logger.LogRequest(c, duration, nil)
	}
}

func GetLoggerFromContext(c *gin.Context) *Logger {
	logger := GetLogger()
	return logger.WithRequestID(
		c.GetHeader("X-Request-ID"),
	).WithClientIP(
		c.ClientIP(),
	).WithUserAgent(
		c.Request.UserAgent(),
	)
}
