package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/slate"
)

type registerSlateMaterialRequest struct {
	Name          string    `json:"name"`
	ContentURI    string    `json:"content_uri"`
	Checksum      string    `json:"checksum"`
	ControlledBy  string    `json:"controlled_by"`
	AvailableFrom time.Time `json:"available_from"`
	AvailableTo   time.Time `json:"available_to"`
}

type proposeSlateRequest struct {
	EventID  string `json:"event_id"`
	Reason   string `json:"reason"`
	Operator string `json:"operator"`
}

type cutInSlateRequest struct {
	EventID  string `json:"event_id"`
	SlateID  string `json:"slate_id"`
	Command  string `json:"command"`
	Operator string `json:"operator"`
}

type handoverSlateRequest struct {
	EventID    string `json:"event_id"`
	TakeoverID string `json:"takeover_id"`
	Director   string `json:"director"`
}

func decodeSlatePayload(writer http.ResponseWriter, request *http.Request, target any) bool {
	defer request.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid slate payload")
		return false
	}
	return true
}

func (server *Server) listSlateMaterials(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"materials": server.runtime.SlateMaterials()})
}

func (server *Server) registerSlateMaterial(writer http.ResponseWriter, request *http.Request) {
	var input registerSlateMaterialRequest
	if !decodeSlatePayload(writer, request, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ContentURI = strings.TrimSpace(input.ContentURI)
	input.Checksum = strings.TrimSpace(input.Checksum)
	input.ControlledBy = strings.TrimSpace(input.ControlledBy)
	if input.Name == "" || input.ContentURI == "" || input.Checksum == "" || input.ControlledBy == "" {
		writeError(writer, http.StatusBadRequest, "slate material name, content_uri, checksum and controlled_by are required")
		return
	}
	material, err := server.coordinator.RegisterSlateMaterial(slate.Material{
		Name:          input.Name,
		ContentURI:    input.ContentURI,
		Checksum:      input.Checksum,
		ControlledBy:  input.ControlledBy,
		AvailableFrom: input.AvailableFrom,
		AvailableTo:   input.AvailableTo,
	})
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, material)
}

func (server *Server) listSlateEmergencies(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"emergencies": server.runtime.SlateEmergencies()})
}

func (server *Server) slateStatus(writer http.ResponseWriter, request *http.Request) {
	eventID := model.EventID(strings.TrimSpace(request.URL.Query().Get("event_id")))
	view, err := server.runtime.SlateStatus(eventID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (server *Server) proposeSlate(writer http.ResponseWriter, request *http.Request) {
	var input proposeSlateRequest
	if !decodeSlatePayload(writer, request, &input) {
		return
	}
	input.Operator = strings.TrimSpace(input.Operator)
	if input.EventID == "" || input.Operator == "" {
		writeError(writer, http.StatusBadRequest, "event_id and operator are required")
		return
	}
	emergency, err := server.coordinator.ProposeSlate(model.EventID(input.EventID), input.Reason, input.Operator)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, emergency)
}

func (server *Server) cutInSlate(writer http.ResponseWriter, request *http.Request) {
	var input cutInSlateRequest
	if !decodeSlatePayload(writer, request, &input) {
		return
	}
	input.Operator = strings.TrimSpace(input.Operator)
	if input.EventID == "" || input.SlateID == "" || input.Command == "" || input.Operator == "" {
		writeError(writer, http.StatusBadRequest, "event_id, slate_id, command and operator are required")
		return
	}
	emergency, err := server.coordinator.ConfirmSlateCutIn(
		model.EventID(input.EventID), input.SlateID, model.CommandID(input.Command), input.Operator)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, emergency)
}

func (server *Server) handoverSlate(writer http.ResponseWriter, request *http.Request) {
	var input handoverSlateRequest
	if !decodeSlatePayload(writer, request, &input) {
		return
	}
	input.Director = strings.TrimSpace(input.Director)
	if input.EventID == "" || input.TakeoverID == "" || input.Director == "" {
		writeError(writer, http.StatusBadRequest, "event_id, takeover_id and director are required")
		return
	}
	emergency, err := server.coordinator.CompleteSlateHandover(model.EventID(input.EventID), input.TakeoverID, input.Director)
	if err != nil {
		writeError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, emergency)
}

func (server *Server) slateEvidence(writer http.ResponseWriter, request *http.Request) {
	eventID := model.EventID(strings.TrimSpace(request.URL.Query().Get("event_id")))
	if !eventID.Valid() {
		writeError(writer, http.StatusBadRequest, "event_id query parameter is invalid")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"evidence": server.runtime.SlateEvidence(eventID)})
}
