package slate

import (
	"fmt"
	"strings"
	"time"

	"github.com/wyw14/solo-035/internal/backup"
	"github.com/wyw14/solo-035/internal/journal"
	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/playout"
	"github.com/wyw14/solo-035/internal/source"
)

const (
	KindCutIn     = "slate-cut-in"
	KindHandover  = "slate-handover"
	KindWithdrawn = "slate-withdrawn"
)

const (
	TakeoverPrimary = "primary"
	TakeoverBackup  = "backup"
)

type Coordinator struct {
	state   *State
	journal *journal.Integration
	sources *source.Coordinator
	playout *playout.State
	backups *backup.State
	clock   func() time.Time
}

func NewCoordinator(state *State, log *journal.Integration, sources *source.Coordinator,
	playoutState *playout.State, backups *backup.State, clock func() time.Time) *Coordinator {
	return &Coordinator{state: state, journal: log, sources: sources, playout: playoutState, backups: backups, clock: clock}
}

type ChainStatus struct {
	Available    bool      `json:"available"`
	Reason       string    `json:"reason"`
	SignalLocked bool      `json:"signal_locked"`
	PlayoutOnAir bool      `json:"playout_on_air"`
	BackupStatus string    `json:"backup_status,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

type TakeoverOption struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail"`
}

type StatusView struct {
	Emergency  *Emergency       `json:"emergency,omitempty"`
	Chain      ChainStatus      `json:"chain"`
	Candidates []Material       `json:"candidates"`
	Takeovers  []TakeoverOption `json:"takeovers"`
}

func (coordinator *Coordinator) evaluate(eventID model.EventID) ChainStatus {
	now := coordinator.clock().UTC()
	chain := ChainStatus{CheckedAt: now}
	output, hasOutput := coordinator.playout.Output(eventID)
	if hasOutput {
		chain.PlayoutOnAir = output.OnAir
		chain.SignalLocked = output.OnAir && coordinator.sources.Ready(output.Source)
	}
	if failover, ok := coordinator.backups.Get(eventID); ok {
		chain.BackupStatus = failover.Status
	}
	backupActive := chain.BackupStatus == "active"
	primaryReady := chain.SignalLocked
	switch {
	case primaryReady:
		chain.Available = true
		chain.Reason = "primary playout is on air with full media lock"
	case backupActive:
		chain.Available = true
		chain.Reason = "backup failover path is providing the program"
	case !hasOutput:
		chain.Reason = "no playout output exists and the backup path is inactive"
	default:
		chain.Reason = "external signal is unlocked and the backup path is inactive"
	}
	return chain
}

func (coordinator *Coordinator) takeoverOptions(eventID model.EventID) []TakeoverOption {
	chain := coordinator.evaluate(eventID)
	options := []TakeoverOption{
		{ID: TakeoverPrimary, Label: "主路播出输出", Ready: chain.SignalLocked,
			Detail: "primary playout output with full audio/video lock"},
		{ID: TakeoverBackup, Label: "音画备路输出", Ready: chain.BackupStatus == "active",
			Detail: "backup failover encoder path"},
	}
	if chain.SignalLocked {
		options[0].Detail = "primary media lock is confirmed"
	}
	if chain.BackupStatus == "active" {
		options[1].Detail = "backup failover is active"
	}
	return options
}

func (coordinator *Coordinator) RegisterMaterial(material Material) (Material, error) {
	return coordinator.state.RegisterMaterial(material, coordinator.clock())
}

func (coordinator *Coordinator) Materials() []Material {
	return coordinator.state.ListMaterials()
}

func (coordinator *Coordinator) Emergencies() []Emergency {
	return coordinator.state.ListEmergencies()
}

func (coordinator *Coordinator) eligibleMaterials() []Material {
	now := coordinator.clock().UTC()
	materials := coordinator.state.ListMaterials()
	eligible := make([]Material, 0, len(materials))
	for _, material := range materials {
		if material.Eligible(now) {
			eligible = append(eligible, material)
		}
	}
	return eligible
}

func (coordinator *Coordinator) Propose(event model.LiveEvent, reason string, operator string) (Emergency, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return Emergency{}, fmt.Errorf("slate proposal requires the proposing operator identity")
	}
	if chain := coordinator.evaluate(event.ID); chain.Available {
		return Emergency{}, fmt.Errorf("playout chain is still available (%s); slate cut-in is not permitted", chain.Reason)
	}
	materials := coordinator.eligibleMaterials()
	if len(materials) == 0 {
		return Emergency{}, fmt.Errorf("no controlled slate material is within its applicable window")
	}
	candidateIDs := make([]string, 0, len(materials))
	for _, material := range materials {
		candidateIDs = append(candidateIDs, material.ID)
	}
	emergency, err := coordinator.state.BeginProposal(Emergency{
		EventID:    event.ID,
		Reason:     strings.TrimSpace(reason),
		ProposedBy: operator,
		Candidates: candidateIDs,
	}, coordinator.clock())
	if err != nil {
		return Emergency{}, err
	}
	return emergency, nil
}

func (coordinator *Coordinator) ConfirmCutIn(event model.LiveEvent, slateID string, command model.CommandID, operator string) (Emergency, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return Emergency{}, fmt.Errorf("slate cut-in requires the confirming operator identity")
	}
	emergency, ok := coordinator.state.GetEmergency(event.ID)
	if !ok {
		return Emergency{}, fmt.Errorf("no slate emergency proposal exists for event %s", event.ID)
	}
	if emergency.Status != StatusProposed {
		return Emergency{}, fmt.Errorf("slate emergency for event %s is %s, not awaiting confirmation", event.ID, emergency.Status)
	}
	if command == "" || command != emergency.Command {
		return Emergency{}, fmt.Errorf("slate confirmation token does not match the active proposal")
	}
	allowed := false
	for _, candidate := range emergency.Candidates {
		if candidate == slateID {
			allowed = true
			break
		}
	}
	if !allowed {
		return Emergency{}, fmt.Errorf("slate material %s was not among the confirmed proposal candidates", slateID)
	}
	material, ok := coordinator.state.Material(slateID)
	if !ok {
		return Emergency{}, fmt.Errorf("slate material %s is not registered", slateID)
	}
	now := coordinator.clock().UTC()
	if !material.Eligible(now) {
		return Emergency{}, fmt.Errorf("slate material %s is outside its applicable window", slateID)
	}
	if chain := coordinator.evaluate(event.ID); chain.Available {
		return Emergency{}, fmt.Errorf("playout chain recovered before slate confirmation (%s); proposal is withdrawn", chain.Reason)
	}
	if _, err := coordinator.journal.Persist(event.ID, KindCutIn, event.Revision, event.CommandEpoch, map[string]any{
		"slate_id":   material.ID,
		"slate_name": material.Name,
		"operator":   operator,
		"reason":     emergency.Reason,
		"command":    string(emergency.Command),
	}); err != nil {
		return Emergency{}, err
	}
	emergency.Status = StatusOnAir
	emergency.SlateID = material.ID
	emergency.SlateName = material.Name
	emergency.ConfirmedBy = operator
	cutInAt := now
	emergency.CutInAt = &cutInAt
	coordinator.state.UpdateEmergency(emergency)
	return emergency, nil
}

func (coordinator *Coordinator) Handover(event model.LiveEvent, takeoverID string, director string) (Emergency, error) {
	director = strings.TrimSpace(director)
	if director == "" {
		return Emergency{}, fmt.Errorf("slate handover requires the director identity")
	}
	emergency, ok := coordinator.state.GetEmergency(event.ID)
	if !ok {
		return Emergency{}, fmt.Errorf("no slate emergency exists for event %s", event.ID)
	}
	if emergency.Status != StatusOnAir {
		return Emergency{}, fmt.Errorf("slate emergency for event %s is %s, not on air", event.ID, emergency.Status)
	}
	var selected TakeoverOption
	for _, option := range coordinator.takeoverOptions(event.ID) {
		if option.ID == takeoverID {
			selected = option
			break
		}
	}
	if selected.ID == "" {
		return Emergency{}, fmt.Errorf("takeover output %s is not a recognized output", takeoverID)
	}
	if !selected.Ready {
		return Emergency{}, fmt.Errorf("takeover output %s is not ready to accept the program", selected.Label)
	}
	if _, err := coordinator.journal.Persist(event.ID, KindHandover, event.Revision, event.CommandEpoch, map[string]any{
		"takeover_id":    selected.ID,
		"takeover_label": selected.Label,
		"director":       director,
	}); err != nil {
		return Emergency{}, err
	}
	if _, err := coordinator.journal.Persist(event.ID, KindWithdrawn, event.Revision, event.CommandEpoch, map[string]any{
		"slate_id":   emergency.SlateID,
		"slate_name": emergency.SlateName,
		"director":   director,
	}); err != nil {
		return Emergency{}, err
	}
	now := coordinator.clock().UTC()
	emergency.Status = StatusHandedOver
	emergency.TakeoverID = selected.ID
	emergency.TakeoverName = selected.Label
	emergency.Director = director
	emergency.HandedOverAt = &now
	coordinator.state.UpdateEmergency(emergency)
	return emergency, nil
}

func (coordinator *Coordinator) Status(eventID model.EventID) StatusView {
	view := StatusView{
		Chain:      coordinator.evaluate(eventID),
		Candidates: coordinator.eligibleMaterials(),
		Takeovers:  coordinator.takeoverOptions(eventID),
	}
	if emergency, ok := coordinator.state.GetEmergency(eventID); ok {
		view.Emergency = &emergency
	}
	return view
}
