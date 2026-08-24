// Package dashboard serves IAPStack's embedded, same-origin operations interface.
package dashboard

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
)

const (
	// dashboardPath is the canonical trailing-slash entrypoint for relative assets.
	dashboardPath = "/dashboard/"
	// indexAsset is the embedded dashboard document.
	indexAsset = "index.html"
)

// Handler serves immutable dashboard assets and delegates all other requests.
type Handler struct {
	next http.Handler
}

var (
	// assets contains the dependency-free dashboard document, styles, and behavior.
	//go:embed index.html app.css app.js
	assets embed.FS
)

// New constructs a same-origin dashboard handler around the versioned API.
func New(next http.Handler) (*Handler, error) {
	if next == nil {
		return nil, errors.New("dashboard API handler is required")
	}
	return &Handler{next: next}, nil
}

// ServeHTTP serves dashboard assets and delegates probes and API traffic unchanged.
func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/dashboard" {
		http.Redirect(writer, request, dashboardPath, http.StatusPermanentRedirect)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == dashboardPath {
		handler.serveAsset(writer, indexAsset)
		return
	}
	if request.Method == http.MethodGet {
		switch request.URL.Path {
		case "/dashboard/app.css":
			handler.serveAsset(writer, "app.css")
			return
		case "/dashboard/app.js":
			handler.serveAsset(writer, "app.js")
			return
		}
	}
	handler.next.ServeHTTP(writer, request)
}

// serveAsset writes one allow-listed embedded asset with strict browser security headers.
func (handler *Handler) serveAsset(writer http.ResponseWriter, name string) {
	payload, err := fs.ReadFile(assets, name)
	if err != nil {
		http.Error(writer, "dashboard asset unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("X-Frame-Options", "DENY")
	switch name {
	case indexAsset:
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	case "app.css":
		writer.Header().Set("Content-Type", "text/css; charset=utf-8")
	case "app.js":
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(payload)
}
