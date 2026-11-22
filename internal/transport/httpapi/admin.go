package httpapi

import (
	"net/http"

	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
)

// adminProjects returns the bounded project navigation collection.
func (api *API) adminProjects(writer http.ResponseWriter, request *http.Request) {
	actor, ok := api.requireAdminActor(writer, request)
	if !ok {
		return
	}
	projects, err := api.controlPlane().Projects(request.Context(), actor)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, projects)
}

// adminProjectOverview returns one project-scoped, secret-free operational snapshot.
func (api *API) adminProjectOverview(writer http.ResponseWriter, request *http.Request) {
	actor, ok := api.requireAdminActor(writer, request)
	if !ok {
		return
	}
	values := request.URL.Query()
	overview, err := api.controlPlane().ProjectOverview(request.Context(), actor, controlplane.ProjectOverviewInput{
		ProjectID:            core.ProjectID(request.PathValue("project_id")),
		FinancialPeriodStart: values.Get("financial_period_start"),
		FinancialPeriodEnd:   values.Get("financial_period_end"),
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, overview)
}

// controlPlane returns the shared control-plane service over this API's dependencies.
// It is built per call so tests that assemble a partial API keep working.
func (api *API) controlPlane() *controlplane.Service {
	dependencies := controlplane.Dependencies{
		Store: api.store, Operations: api.operations, Admin: api.admin, Protection: api.protection, Clock: api.now,
	}
	if api.financialReports != nil {
		dependencies.FinancialReports = api.financialReports
	}
	return controlplane.New(dependencies)
}
