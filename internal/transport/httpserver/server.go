package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
)

type Server struct {
	logger          *slog.Logger
	server          *http.Server
	shutdownTimeout time.Duration
	ready           atomic.Bool
}

func New(address string, shutdownTimeout time.Duration, logger *slog.Logger) *Server {
	server := &Server{
		logger:          logger,
		shutdownTimeout: shutdownTimeout,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.readiness)
	server.server = &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	return server
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.server.Addr, err)
	}

	s.ready.Store(true)
	s.logger.Info("api is ready", "address", listener.Addr().String())

	serveResult := make(chan error, 1)
	go func() {
		err := s.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveResult <- err
	}()

	select {
	case err := <-serveResult:
		s.ready.Store(false)
		if err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.ready.Store(false)
		s.logger.Info("shutting down api")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
		closeErr := s.server.Close()
		return errors.Join(fmt.Errorf("shutdown HTTP server: %w", err), closeErr)
	}

	if err := <-serveResult; err != nil {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}

	return nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

func (s *Server) readiness(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		writeStatus(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	writeStatus(w, http.StatusOK, "ready")
}

func writeStatus(w http.ResponseWriter, statusCode int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprintf(w, "{\"status\":%q}\n", status)
}
