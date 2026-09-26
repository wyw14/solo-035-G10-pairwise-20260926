package api

import (
	"net/http"

	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/service"
)

func (server *Server) emergency(writer http.ResponseWriter, request *http.Request) {
	eventID := model.EventID(request.URL.Query().Get("event_id"))
	coordinator := service.NewCoordinator(server.runtime)
	writeJSON(writer, http.StatusOK, coordinator.EmergencyOverview(eventID))
}
