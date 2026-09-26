package slate

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/solo-035/internal/model"
)

type Status string

const (
	StatusProposed   Status = "proposed"
	StatusOnAir      Status = "on-air"
	StatusHandedOver Status = "handed-over"
)

type Material struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ContentURI    string    `json:"content_uri"`
	Checksum      string    `json:"checksum"`
	ControlledBy  string    `json:"controlled_by"`
	AvailableFrom time.Time `json:"available_from,omitempty"`
	AvailableTo   time.Time `json:"available_to,omitempty"`
	Controlled    bool      `json:"controlled"`
	CreatedAt     time.Time `json:"created_at"`
}

func (material Material) Eligible(now time.Time) bool {
	stamp := now.UTC()
	if !material.AvailableFrom.IsZero() && stamp.Before(material.AvailableFrom.UTC()) {
		return false
	}
	if !material.AvailableTo.IsZero() && stamp.After(material.AvailableTo.UTC()) {
		return false
	}
	return true
}

type Emergency struct {
	EventID      model.EventID   `json:"event_id"`
	Command      model.CommandID `json:"command"`
	Status       Status          `json:"status"`
	Reason       string          `json:"reason"`
	SlateID      string          `json:"slate_id"`
	SlateName    string          `json:"slate_name"`
	ProposedBy   string          `json:"proposed_by"`
	ConfirmedBy  string          `json:"confirmed_by"`
	TakeoverID   string          `json:"takeover_id"`
	TakeoverName string          `json:"takeover_name"`
	Director     string          `json:"director"`
	Candidates   []string        `json:"candidates"`
	ProposedAt   time.Time       `json:"proposed_at"`
	CutInAt      *time.Time      `json:"cut_in_at,omitempty"`
	HandedOverAt *time.Time      `json:"handed_over_at,omitempty"`
}

type State struct {
	mu          sync.RWMutex
	materials   map[string]Material
	emergencies map[model.EventID]Emergency
}

func NewState() *State {
	return &State{materials: make(map[string]Material), emergencies: make(map[model.EventID]Emergency)}
}

func (state *State) RegisterMaterial(material Material, now time.Time) (Material, error) {
	if material.Name == "" || material.ContentURI == "" || material.Checksum == "" || material.ControlledBy == "" {
		return Material{}, fmt.Errorf("controlled slate material is missing required identity fields")
	}
	if !material.AvailableFrom.IsZero() && !material.AvailableTo.IsZero() &&
		material.AvailableTo.UTC().Before(material.AvailableFrom.UTC()) {
		return Material{}, fmt.Errorf("slate material applicable window ends before it starts")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if material.ID == "" {
		material.ID = uuid.NewString()
	} else if _, exists := state.materials[material.ID]; exists {
		return Material{}, fmt.Errorf("slate material %s is already registered", material.ID)
	}
	material.Controlled = true
	material.CreatedAt = now.UTC()
	state.materials[material.ID] = material
	return material, nil
}

func (state *State) Material(id string) (Material, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	material, ok := state.materials[id]
	return material, ok
}

func (state *State) ListMaterials() []Material {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]Material, 0, len(state.materials))
	for _, material := range state.materials {
		items = append(items, material)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (state *State) BeginProposal(emergency Emergency, now time.Time) (Emergency, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if current, ok := state.emergencies[emergency.EventID]; ok && current.Status != StatusHandedOver {
		return Emergency{}, fmt.Errorf("event %s already has a %s slate emergency", emergency.EventID, current.Status)
	}
	if len(emergency.Candidates) == 0 {
		return Emergency{}, fmt.Errorf("slate proposal requires at least one candidate material")
	}
	emergency.Command = model.NewCommandID()
	emergency.Status = StatusProposed
	emergency.ProposedAt = now.UTC()
	state.emergencies[emergency.EventID] = emergency
	return emergency, nil
}

func (state *State) GetEmergency(eventID model.EventID) (Emergency, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	emergency, ok := state.emergencies[eventID]
	return emergency, ok
}

func (state *State) UpdateEmergency(emergency Emergency) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.emergencies[emergency.EventID] = emergency
}

func (state *State) ListEmergencies() []Emergency {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]Emergency, 0, len(state.emergencies))
	for _, emergency := range state.emergencies {
		items = append(items, emergency)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ProposedAt.Before(items[j].ProposedAt) })
	return items
}

func (state *State) ReplaceMaterials(materials []Material) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.materials = make(map[string]Material, len(materials))
	for _, material := range materials {
		state.materials[material.ID] = material
	}
}

func (state *State) ReplaceEmergencies(emergencies []Emergency) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.emergencies = make(map[model.EventID]Emergency, len(emergencies))
	for _, emergency := range emergencies {
		state.emergencies[emergency.EventID] = emergency
	}
}
