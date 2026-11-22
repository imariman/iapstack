package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/validation"
)

// adminSubscriptionMetrics returns subscription health derived from stored purchase observations.
func (api *API) adminSubscriptionMetrics(writer http.ResponseWriter, request *http.Request) {
	actor, ok := api.requireAdminActor(writer, request)
	if !ok {
		return
	}
	values := request.URL.Query()
	input := controlplane.SubscriptionMetricsInput{
		ProjectID:   core.ProjectID(request.PathValue("project_id")),
		Environment: core.Environment(values.Get("environment")),
	}
	if raw := values.Get("window_days"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil {
			api.writeError(writer, request, validation.Wrap(errors.New("window_days must be an integer")))
			return
		}
		input.WindowDays = &days
	}
	metrics, err := api.controlPlane().SubscriptionMetrics(request.Context(), actor, input)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, metrics)
}
