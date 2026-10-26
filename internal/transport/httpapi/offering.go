package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/offering"
	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// offeringCacheControl is the public cache policy for one offering document.
	offeringCacheControl = "public, max-age=300"
)

// getApplicationOffering returns one public offering document.
// The query string is ignored, so it cannot select a customer.
func (api *API) getApplicationOffering(writer http.ResponseWriter, request *http.Request) {
	document, err := offering.Load(
		request.Context(),
		api.offeringDirectory,
		core.ApplicationID(request.PathValue("application_id")),
		api.offeringCatalog,
	)
	if err != nil {
		if errors.Is(err, persistence.ErrUnavailable) {
			api.writeError(writer, request, err)
			return
		}
		if errors.Is(err, offering.ErrNotFound) || errors.Is(err, offering.ErrRejected) {
			api.notFound(writer, request)
			return
		}
		api.writeError(writer, request, err)
		return
	}
	writeOffering(writer, document)
}

// writeOffering writes one offering document with its revision cache headers.
func writeOffering(writer http.ResponseWriter, document offering.Document) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", offeringCacheControl)
	writer.Header().Set("ETag", strconv.Quote(document.Revision))
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(document)
}
