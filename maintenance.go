// Package traefik_maintenance_warden provides a Traefik plugin to redirect traffic to a maintenance page
// while allowing requests with a specific header to bypass the redirection
package traefik_maintenance_warden

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// LogLevel defines the level of logging
type LogLevel int

const (
	// LogLevelNone disables logging
	LogLevelNone LogLevel = iota
	// LogLevelError logs only errors
	LogLevelError
	// LogLevelInfo logs info and errors
	LogLevelInfo
	// LogLevelDebug logs debug, info and errors
	LogLevelDebug
)

// Config holds the plugin configuration.
type Config struct {
	// MaintenanceService is the URL of the maintenance service to redirect to
	MaintenanceService string `json:"maintenanceService,omitempty"`

	// MaintenanceFilePath is the path to a static HTML file to serve instead of redirecting
	MaintenanceFilePath string `json:"maintenanceFilePath,omitempty"`

	// MaintenanceContent is the direct HTML content to serve instead of a file or service
	MaintenanceContent string `json:"maintenanceContent,omitempty"`

	// BypassHeader is the header name that allows bypassing maintenance mode
	BypassHeader string `json:"bypassHeader,omitempty"`

	// BypassHeaderValue is the expected value of the bypass header
	BypassHeaderValue string `json:"bypassHeaderValue,omitempty"`

	// BypassJWTTokenHeader is the header containing the JWT token
	BypassJWTTokenHeader string `json:"bypassJWTTokenHeader,omitempty"`

	// BypassJWTTokenClaim is the claim name in the JWT token that contains the bypass value
	BypassJWTTokenClaim string `json:"bypassJWTTokenClaim,omitempty"`

	// BypassJWTTokenClaimValue is the expected value of the JWT token claim
	BypassJWTTokenClaimValue string `json:"bypassJWTTokenClaimValue,omitempty"`

	// Enabled controls whether the maintenance mode is active
	Enabled bool `json:"enabled,omitempty"`

	// StatusCode is the HTTP status code to return when in maintenance mode
	StatusCode int `json:"statusCode,omitempty"`

	// BypassPaths are paths that should bypass maintenance mode
	BypassPaths []string `json:"bypassPaths,omitempty"`

	// BypassFavicon controls whether favicon.ico requests bypass maintenance mode
	BypassFavicon bool `json:"bypassFavicon,omitempty"`

	// LogLevel controls the verbosity of logging (0=none, 1=error, 2=info, 3=debug)
	LogLevel int `json:"logLevel,omitempty"`

	// MaintenanceTimeout is the timeout for requests to the maintenance service in seconds
	MaintenanceTimeout int `json:"maintenanceTimeout,omitempty"`

	// ContentType is the content type header to set when serving the maintenance file
	ContentType string `json:"contentType,omitempty"`

	// EnabledAnnotation is the Kubernetes annotation name that controls the enabled state
	EnabledAnnotation string `json:"enabledAnnotation,omitempty"`

	// EnabledAnnotationValue is the expected value of the enabled annotation
	EnabledAnnotationValue string `json:"enabledAnnotationValue,omitempty"`

	// EnabledAnnotationHeader is the header that Traefik adds with the annotation value
	EnabledAnnotationHeader string `json:"enabledAnnotationHeader,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		MaintenanceService:      "",
		MaintenanceFilePath:     "",
		MaintenanceContent:      "",
		BypassHeader:            "X-Maintenance-Bypass",
		BypassHeaderValue:       "true",
		BypassJWTTokenHeader:    "Authorization",
		BypassJWTTokenClaim:     "",
		BypassJWTTokenClaimValue: "",
		Enabled:                 true,
		StatusCode:              503,
		BypassPaths:             []string{},
		BypassFavicon:           true,
		LogLevel:                int(LogLevelError),
		MaintenanceTimeout:      10,
		ContentType:             "text/html; charset=utf-8",
		EnabledAnnotation:       "",
		EnabledAnnotationValue:  "true",
		EnabledAnnotationHeader: "",
	}
}

// MaintenanceBypass is a middleware that redirects all traffic to a maintenance page
// unless the request has a specific bypass header.
type MaintenanceBypass struct {
	next                   http.Handler
	maintenanceService     *url.URL
	maintenanceFilePath    string
	maintenanceFileContent []byte
	maintenanceContent     string
	maintenanceFileLastMod time.Time
	fileMutex              sync.RWMutex
	bypassHeader           string
	bypassHeaderValue      string
	bypassJWTTokenHeader   string
	bypassJWTTokenClaim    string
	bypassJWTTokenClaimValue string
	enabled                bool
	statusCode             int
	bypassPaths            []string
	bypassFavicon          bool
	name                   string
	logger                 *log.Logger
	logLevel               LogLevel
	timeout                time.Duration
	contentType            string
	enabledAnnotation      string
	enabledAnnotationValue string
	enabledAnnotationHeader string
}

// New creates a new MaintenanceBypass middleware.
func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	// Default to 503 Service Unavailable if not specified
	statusCode := config.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusServiceUnavailable
	}

	// Default content type if not specified
	contentType := config.ContentType
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}

	// Create logger
	logger := log.New(os.Stdout, "[maintenance-warden] ", log.LstdFlags)

	// Create the middleware instance
	m := &MaintenanceBypass{
		next:                   next,
		maintenanceFilePath:    config.MaintenanceFilePath,
		maintenanceContent:     config.MaintenanceContent,
		bypassHeader:           config.BypassHeader,
		bypassHeaderValue:      config.BypassHeaderValue,
		bypassJWTTokenHeader:   config.BypassJWTTokenHeader,
		bypassJWTTokenClaim:    config.BypassJWTTokenClaim,
		bypassJWTTokenClaimValue: config.BypassJWTTokenClaimValue,
		enabled:                config.Enabled,
		statusCode:             statusCode,
		bypassPaths:            config.BypassPaths,
		bypassFavicon:          config.BypassFavicon,
		name:                   name,
		logger:                 logger,
		logLevel:               LogLevel(config.LogLevel),
		contentType:            contentType,
		enabledAnnotation:      config.EnabledAnnotation,
		enabledAnnotationValue: config.EnabledAnnotationValue,
		enabledAnnotationHeader: config.EnabledAnnotationHeader,
	}

	// If maintenance file path is specified, try to read it initially
	if config.MaintenanceFilePath != "" {
		err := m.loadMaintenanceFile()
		if err != nil {
			return nil, fmt.Errorf("failed to load maintenance file: %w", err)
		}
	} else if config.MaintenanceContent != "" {
		// If direct content is provided, use that
		m.log(LogLevelInfo, "Using provided maintenance content (%d bytes)", len(config.MaintenanceContent))
	} else if config.MaintenanceService != "" {
		// Validate maintenance service URL
		maintenanceURL, err := url.Parse(config.MaintenanceService)
		if err != nil {
			return nil, fmt.Errorf("invalid maintenance service URL: %w", err)
		}

		if maintenanceURL.Scheme == "" || maintenanceURL.Host == "" {
			return nil, fmt.Errorf("maintenance service URL must include scheme and host")
		}

		// Set default timeout if not specified
		timeout := time.Duration(config.MaintenanceTimeout) * time.Second
		if timeout == 0 {
			timeout = 10 * time.Second
		}

		m.maintenanceService = maintenanceURL
		m.timeout = timeout
	} else {
		return nil, fmt.Errorf("either maintenanceService, maintenanceFilePath, or maintenanceContent must be specified")
	}

	return m, nil
}

// loadMaintenanceFile reads the maintenance HTML file from disk
func (m *MaintenanceBypass) loadMaintenanceFile() error {
	m.fileMutex.Lock()
	defer m.fileMutex.Unlock()

	fileInfo, err := os.Stat(m.maintenanceFilePath)
	if err != nil {
		return fmt.Errorf("error accessing maintenance file: %w", err)
	}

	// Only reload if file is newer than our last modification time
	if m.maintenanceFileContent != nil && !fileInfo.ModTime().After(m.maintenanceFileLastMod) {
		return nil
	}

	content, err := ioutil.ReadFile(m.maintenanceFilePath)
	if err != nil {
		return fmt.Errorf("error reading maintenance file: %w", err)
	}

	// Check if the file is empty
	if len(content) == 0 {
		return fmt.Errorf("maintenance file is empty: %s", m.maintenanceFilePath)
	}

	m.maintenanceFileContent = content
	m.maintenanceFileLastMod = fileInfo.ModTime()
	m.log(LogLevelInfo, "Loaded maintenance file: %s (%d bytes)", m.maintenanceFilePath, len(content))

	return nil
}

// log logs a message at the specified level
func (m *MaintenanceBypass) log(level LogLevel, format string, v ...interface{}) {
	if level <= m.logLevel {
		m.logger.Printf(format, v...)
	}
}

// isMaintenanceEnabled checks if maintenance mode is enabled for this request
// taking into account both the static configuration and any dynamic annotation
func (m *MaintenanceBypass) isMaintenanceEnabled(req *http.Request) bool {
	// If annotation-based configuration is enabled, check for annotation
	if m.enabledAnnotation != "" && m.enabledAnnotationHeader != "" {
		// Check for the annotation value in the header
		annotationHeader := req.Header.Get(m.enabledAnnotationHeader)
		m.log(LogLevelDebug, "Checking annotation header: %s = %s", m.enabledAnnotationHeader, annotationHeader)
		
		// Check if the annotation exists with the right value
		annotationWithValue := fmt.Sprintf("%s=%s", m.enabledAnnotation, m.enabledAnnotationValue)
		if strings.Contains(annotationHeader, annotationWithValue) {
			m.log(LogLevelDebug, "Found annotation %s with value %s, maintenance mode enabled", 
				m.enabledAnnotation, m.enabledAnnotationValue)
			return true
		}
		
		// If we're using annotation control and the annotation doesn't match, use the static config
		m.log(LogLevelDebug, "Annotation control enabled but value not found or not matching, using static config: %v", m.enabled)
	}
	
	// No annotation control or no match, use the static configuration
	return m.enabled
}

// ServeHTTP implements the http.Handler interface.
func (m *MaintenanceBypass) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Check if maintenance mode is enabled, considering annotations if configured
	enabled := m.isMaintenanceEnabled(req)
	
	// If maintenance mode is disabled, simply pass to the next handler
	if !enabled {
		m.log(LogLevelDebug, "Maintenance mode is disabled, passing request through: %s", req.URL.String())
		m.next.ServeHTTP(rw, req)
		return
	}

	// Check if the request is for favicon.ico and should bypass
	if m.bypassFavicon && strings.HasSuffix(req.URL.Path, "/favicon.ico") {
		m.log(LogLevelDebug, "Request is for favicon.ico, bypassing maintenance mode: %s", req.URL.String())
		m.next.ServeHTTP(rw, req)
		return
	}

	// Check if the request path is in the bypass paths list
	for _, path := range m.bypassPaths {
		if strings.HasPrefix(req.URL.Path, path) {
			m.log(LogLevelDebug, "Request path %s matches bypass path %s, passing through", req.URL.Path, path)
			m.next.ServeHTTP(rw, req)
			return
		}
	}

	// Check if the request has the bypass header with the correct value
	// Only check if bypassHeader is configured
	if m.bypassHeader != "" {
		headerValue := req.Header.Get(m.bypassHeader)
		if headerValue == m.bypassHeaderValue {
			// If the bypass header is present with the correct value, pass the request to the next handler
			m.log(LogLevelDebug, "Bypass header found with value %s, passing to next handler", headerValue)
			m.next.ServeHTTP(rw, req)
			return
		}
	}
	
	// Check if JWT token has the bypass claim with the correct value
	// Only check if bypassJWTTokenHeader and bypassJWTTokenClaim are configured
	if m.bypassJWTTokenHeader != "" && m.bypassJWTTokenClaim != "" && m.bypassJWTTokenClaimValue != "" {
		// Get the JWT token from the header
		authHeader := req.Header.Get(m.bypassJWTTokenHeader)
		if authHeader != "" {
			// For Authorization headers, strip the "Bearer " prefix if present
			tokenString := authHeader
			if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
				tokenString = authHeader[7:]
			}
			
			// Parse and validate the JWT token
			claimValue, err := m.getJWTClaimValue(tokenString, m.bypassJWTTokenClaim)
			if err != nil {
				m.log(LogLevelDebug, "Error parsing JWT token: %v", err)
			} else if claimValue == m.bypassJWTTokenClaimValue {
				// If JWT token has the bypass claim with the correct value, pass the request to the next handler
				m.log(LogLevelDebug, "JWT token bypass claim found with value %s, passing to next handler", claimValue)
				m.next.ServeHTTP(rw, req)
				return
			}
		}
	}

	// No bypass condition met, serve the maintenance page
	m.log(LogLevelInfo, "Serving maintenance page for %s", req.URL.String())

	// Set all common maintenance-related headers here
	rw.Header().Set("X-Maintenance-Mode", "true")
	rw.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	rw.Header().Set("Retry-After", "3600") // Suggest client retry after 1 hour
	rw.Header().Set("Content-Type", m.contentType)
	
	// Determine which maintenance content to serve
	if m.maintenanceContent != "" {
		// If inline content is provided, serve that
		m.serveMaintenanceContent(rw, req)
	} else if m.maintenanceFilePath != "" {
		// If a file path is provided, serve the file
		m.serveMaintenanceFile(rw, req)
	} else if m.maintenanceService != nil {
		// If a maintenance service is configured, proxy to it
		m.proxyToMaintenanceService(rw, req)
	} else {
		// This should never happen as the configuration is validated in New()
		rw.WriteHeader(m.statusCode)
		rw.Write([]byte("Service temporarily unavailable"))
	}
}

// serveMaintenanceFile serves the static maintenance file
func (m *MaintenanceBypass) serveMaintenanceFile(rw http.ResponseWriter, req *http.Request) {
	// Try to reload the file if it's changed (check file modification time)
	err := m.loadMaintenanceFile()
	if err != nil {
		m.log(LogLevelError, "Failed to load maintenance file: %v", err)
		http.Error(rw, "Service Temporarily Unavailable", m.statusCode)
		return
	}

	// Read the content from our cache
	m.fileMutex.RLock()
	content := m.maintenanceFileContent
	m.fileMutex.RUnlock()

	// Write the status code and content
	rw.WriteHeader(m.statusCode)
	rw.Write(content)
}

// serveMaintenanceContent serves the inline maintenance content
func (m *MaintenanceBypass) serveMaintenanceContent(rw http.ResponseWriter, req *http.Request) {
	// Set the status code
	rw.WriteHeader(m.statusCode)
	
	// Write the content
	_, err := rw.Write([]byte(m.maintenanceContent))
	if err != nil {
		m.log(LogLevelError, "Error writing maintenance content: %v", err)
	}
}

// proxyToMaintenanceService proxies the request to the maintenance service
func (m *MaintenanceBypass) proxyToMaintenanceService(rw http.ResponseWriter, req *http.Request) {
	// Create a custom response writer that will set our status code
	maintenanceWriter := &maintenanceResponseWriter{
		ResponseWriter: rw,
		statusCode:     m.statusCode,
	}

	// Create a reverse proxy to the maintenance service
	proxy := httputil.NewSingleHostReverseProxy(m.maintenanceService)

	// Set a timeout for the proxy
	proxy.Transport = &http.Transport{
		ResponseHeaderTimeout: m.timeout,
	}

	// Handle errors from the maintenance service
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		m.log(LogLevelError, "Error proxying to maintenance service: %v", err)
		// Don't need to set X-Maintenance-Mode here since it's already set in ServeHTTP
		rw.WriteHeader(m.statusCode)
		rw.Write([]byte("Service temporarily unavailable"))
	}

	// Clone the request to avoid modifying the original
	proxyReq := req.Clone(req.Context())

	// Update the cloned request Host to match the maintenance service
	proxyReq.URL.Host = m.maintenanceService.Host
	proxyReq.URL.Scheme = m.maintenanceService.Scheme
	proxyReq.Host = m.maintenanceService.Host

	// Proxy the request to the maintenance service with our custom writer
	proxy.ServeHTTP(maintenanceWriter, proxyReq)
}

// maintenanceResponseWriter is a wrapper for http.ResponseWriter that captures the status code
type maintenanceResponseWriter struct {
	http.ResponseWriter
	statusCode int
	headerSet  bool
}

// WriteHeader captures the status code and passes it to the wrapped ResponseWriter
func (w *maintenanceResponseWriter) WriteHeader(statusCode int) {
	if !w.headerSet {
		w.ResponseWriter.WriteHeader(w.statusCode)
		w.headerSet = true
	}
}

// Write writes the response and sets a default status code if none has been set
func (w *maintenanceResponseWriter) Write(b []byte) (int, error) {
	if !w.headerSet {
		w.WriteHeader(w.statusCode)
	}
	return w.ResponseWriter.Write(b)
}

// getJWTClaimValue extracts a claim value from a JWT token
func (m *MaintenanceBypass) getJWTClaimValue(tokenString string, claimName string) (string, error) {
	// Split the token into parts
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT token format")
	}

	// Decode the payload (second part)
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("error decoding JWT payload: %w", err)
	}

	// Parse the payload
	var claims map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", fmt.Errorf("error parsing JWT claims: %w", err)
	}

	// Extract the claim value
	if value, ok := claims[claimName]; ok {
		// Convert claim value to string
		switch v := value.(type) {
		case string:
			return v, nil
		case float64:
			return fmt.Sprintf("%g", v), nil
		case bool:
			return fmt.Sprintf("%t", v), nil
		default:
			return fmt.Sprintf("%v", v), nil
		}
	}

	return "", fmt.Errorf("claim %s not found in JWT token", claimName)
}
