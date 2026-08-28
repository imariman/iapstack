package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/imariman/iapstack/internal/platform/metrics"
)

const (
	// readHeaderTimeout bounds the time allowed to receive HTTP request headers.
	readHeaderTimeout = 5 * time.Second
	// readTimeout bounds the time allowed to receive one complete HTTP request.
	readTimeout = 30 * time.Second
	// writeTimeout bounds the time allowed to write one complete HTTP response.
	writeTimeout = 30 * time.Second
	// idleTimeout bounds how long an idle keep-alive connection remains open.
	idleTimeout = 60 * time.Second
	// maximumHeaderBytes limits request-line and header memory consumption per connection.
	maximumHeaderBytes = 32 << 10
	// defaultReadinessTimeout bounds one dependency probe for compatibility constructors.
	defaultReadinessTimeout = 2 * time.Second
	// requestIDHeader carries one safe request correlation identity.
	requestIDHeader = "X-Request-ID"
)

// ReadyChecker verifies that a required runtime dependency is currently usable.
type ReadyChecker interface {
	// Ping verifies dependency availability using the supplied bounded context.
	Ping(context.Context) error
}

// Options configures the operational HTTP server and optional business API.
type Options struct {
	Address          string
	ShutdownTimeout  time.Duration
	ReadinessTimeout time.Duration
	Logger           *slog.Logger
	ReadyChecker     ReadyChecker
	Metrics          *metrics.Registry
	Handler          http.Handler
}

// Server owns the API listener, probes, middleware, and graceful lifecycle.
type Server struct {
	logger           *slog.Logger
	server           *http.Server
	shutdownTimeout  time.Duration
	readinessTimeout time.Duration
	readyChecker     ReadyChecker
	metrics          *metrics.Registry
	ready            atomic.Bool
}

// statusRecorder captures the final response status for access logs and metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// New constructs a probe-only API server for backward compatibility and isolated tests.
func New(address string, shutdownTimeout time.Duration, logger *slog.Logger) *Server {
	return NewWithOptions(Options{
		Address:          address,
		ShutdownTimeout:  shutdownTimeout,
		ReadinessTimeout: defaultReadinessTimeout,
		Logger:           logger,
		Metrics:          metrics.New(),
	})
}

// NewWithOptions constructs an operational API server with bounded connection settings.
func NewWithOptions(options Options) *Server {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Metrics == nil {
		options.Metrics = metrics.New()
	}
	if options.ReadinessTimeout <= 0 {
		options.ReadinessTimeout = defaultReadinessTimeout
	}
	server := &Server{
		logger:           options.Logger,
		shutdownTimeout:  options.ShutdownTimeout,
		readinessTimeout: options.ReadinessTimeout,
		readyChecker:     options.ReadyChecker,
		metrics:          options.Metrics,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.readiness)
	mux.HandleFunc("GET /metrics", server.prometheus)
	if options.Handler != nil {
		mux.Handle("/", options.Handler)
	}
	server.server = &http.Server{
		Addr:              options.Address,
		Handler:           server.observe(mux),
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maximumHeaderBytes,
	}

	return server
}

// Run serves HTTP traffic until cancellation or an unrecoverable server failure.
func (server *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", server.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.server.Addr, err)
	}

	server.ready.Store(true)
	server.logger.Info("api is ready", "address", listener.Addr().String())

	serveResult := make(chan error, 1)
	go func() {
		err := server.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveResult <- err
	}()

	select {
	case err := <-serveResult:
		server.ready.Store(false)
		if err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		server.ready.Store(false)
		server.logger.Info("shutting down api")
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), server.shutdownTimeout)
	defer cancel()

	if err := server.server.Shutdown(shutdownCtx); err != nil {
		closeErr := server.server.Close()
		return errors.Join(fmt.Errorf("shutdown HTTP server: %w", err), closeErr)
	}

	if err := <-serveResult; err != nil {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}

	return nil
}

// WriteHeader records the first status code and forwards it to the client.
func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.status == 0 {
		recorder.status = status
	}
	recorder.ResponseWriter.WriteHeader(status)
}

// Write records an implicit success status before forwarding response bytes.
func (recorder *statusRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(body)
}

// health reports whether the API process is alive.
func (server *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeStatus(writer, http.StatusOK, "ok")
}

// readiness reports process state and live dependency availability.
func (server *Server) readiness(writer http.ResponseWriter, request *http.Request) {
	if !server.ready.Load() {
		writeStatus(writer, http.StatusServiceUnavailable, "not_ready")
		return
	}
	if server.readyChecker != nil {
		ctx, cancel := context.WithTimeout(request.Context(), server.readinessTimeout)
		defer cancel()
		if err := server.readyChecker.Ping(ctx); err != nil {
			server.logger.Warn("readiness dependency failed", "error_code", "dependency_unavailable")
			writeStatus(writer, http.StatusServiceUnavailable, "not_ready")
			return
		}
	}
	writeStatus(writer, http.StatusOK, "ready")
}

// prometheus writes a bounded-cardinality operational metrics snapshot.
func (server *Server) prometheus(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", metrics.ContentType())
	if err := server.metrics.WritePrometheus(writer); err != nil {
		server.logger.Error("write metrics", "error_code", "metrics_write_failed")
	}
}

// observe adds request IDs, safe access logs, and HTTP metrics around every route.
func (server *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestID := strings.TrimSpace(request.Header.Get(requestIDHeader))
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}
		writer.Header().Set(requestIDHeader, requestID)
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		recorder := &statusRecorder{ResponseWriter: writer}
		next.ServeHTTP(recorder, request)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(startedAt)
		server.metrics.ObserveHTTP(request.Method, route, recorder.status, elapsed)
		server.logger.Info("http request",
			"request_id", requestID,
			"method", request.Method,
			"route", route,
			"status", recorder.status,
			"duration_ms", elapsed.Milliseconds(),
		)
	})
}

// newRequestID returns a random correlation identity without embedding request data.
func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

// validRequestID accepts short printable correlation IDs and rejects log-breaking input.
func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._:-", character)) {
			return false
		}
	}
	return true
}

// writeStatus writes a small JSON probe response with the requested status code.
func writeStatus(writer http.ResponseWriter, statusCode int, status string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(writer, "{\"status\":%q}\n", status)
}
