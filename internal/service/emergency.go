package service

import (
	"fmt"

	"github.com/wyw14/solo-035/internal/emergency"
	"github.com/wyw14/solo-035/internal/model"
)

// ChainStatus 描述播出链路当前能否提供有效节目。
// 仅当"外部信号失锁，且编码与音画备路也无法提供有效节目"时，
// Available 才为 false，此时才允许走紧急静态片通路。
type ChainStatus struct {
	EventID      model.EventID `json:"event_id"`
	Available    bool          `json:"available"`
	OutputOnAir  bool          `json:"output_on_air"`
	SignalLocked bool          `json:"signal_locked"`
	BackupActive bool          `json:"backup_active"`
	Reasons      []string      `json:"reasons"`
}

// EmergencyView 是应急静态片通路对外的聚合视图。
type EmergencyView struct {
	Chain      ChainStatus           `json:"chain"`
	Candidates []emergency.Candidate `json:"candidates"`
	Session    *emergency.Session    `json:"session,omitempty"`
	Evidence   []emergency.Evidence  `json:"evidence"`
	Assets     []emergency.Asset     `json:"assets"`
	Takeover   *emergency.Takeover   `json:"takeover,omitempty"`
}

func (coordinator *Coordinator) RegisterEmergencyAsset(input emergency.RegisterInput) (emergency.Asset, error) {
	if input.Control == "" {
		return emergency.Asset{}, fmt.Errorf("受控素材必须登记责任人")
	}
	return coordinator.runtime.Emergency.RegisterAsset(input)
}

// EvaluateChain 依据在播输出、外部信号锁定与音画备路状态判断链路是否仍有有效节目。
func (coordinator *Coordinator) EvaluateChain(eventID model.EventID) ChainStatus {
	status := ChainStatus{EventID: eventID}
	if output, ok := coordinator.runtime.Playout.Output(eventID); ok {
		status.OutputOnAir = output.OnAir
	}
	if syncStatus, ok := coordinator.runtime.SyncState.Get(eventID); ok {
		status.SignalLocked = syncStatus.Stable
	}
	if failover, ok := coordinator.runtime.Backup.Status(eventID); ok {
		status.BackupActive = failover.Status == "active"
	}
	if !status.OutputOnAir {
		status.Reasons = append(status.Reasons, "播出输出已停止")
	}
	if !status.SignalLocked {
		status.Reasons = append(status.Reasons, "外部信号失锁")
	}
	if !status.BackupActive {
		status.Reasons = append(status.Reasons, "编码与音画备路未提供有效节目")
	}
	status.Available = status.OutputOnAir && (status.SignalLocked || status.BackupActive)
	return status
}

// EmergencyOverview 汇总某事件的应急通路状态与全局受控素材。
func (coordinator *Coordinator) EmergencyOverview(eventID model.EventID) EmergencyView {
	view := EmergencyView{
		Chain:      coordinator.EvaluateChain(eventID),
		Candidates: coordinator.runtime.Emergency.Candidates(coordinator.runtime.Clock()),
		Assets:     coordinator.runtime.EmergencyState.Assets(),
		Evidence:   coordinator.runtime.EmergencyState.Evidence(eventID),
	}
	if session, ok := coordinator.runtime.EmergencyState.Session(eventID); ok {
		sessionCopy := session
		view.Session = &sessionCopy
	}
	if takeover, ok := coordinator.runtime.EmergencyState.PreparedTakeover(eventID); ok {
		takeoverCopy := takeover
		view.Takeover = &takeoverCopy
	}
	return view
}

// ProposeSlate 在播出链路不可用时给出静态片候选提案（不直接切入）。
func (coordinator *Coordinator) ProposeSlate(eventID model.EventID, assetID model.SlateID, reason string) (emergency.Proposal, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return emergency.Proposal{}, fmt.Errorf("event %s not found", eventID)
	}
	if coordinator.EvaluateChain(eventID).Available {
		return emergency.Proposal{}, fmt.Errorf("播出链路仍可提供有效节目，不应切入紧急静态片")
	}
	return coordinator.runtime.Emergency.Propose(event, assetID, reason)
}

// ConfirmSlate 由导播凭确认令牌明确确认后切入静态片。
func (coordinator *Coordinator) ConfirmSlate(eventID model.EventID, token model.CommandID) (emergency.Evidence, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return emergency.Evidence{}, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Emergency.ConfirmTake(event, token)
}

// WithdrawSlate 撤下正在播出的静态片。
func (coordinator *Coordinator) WithdrawSlate(eventID model.EventID, reason string) (emergency.Evidence, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return emergency.Evidence{}, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Emergency.Withdraw(event, reason)
}

// PrepareTakeover 由导播选择一路已准备好的接管输出。
func (coordinator *Coordinator) PrepareTakeover(eventID model.EventID, label string) (emergency.Takeover, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return emergency.Takeover{}, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Emergency.PrepareTakeover(event, label)
}

// ConfirmHandover 由导播明确确认接管输出，完成交接、结束应急。
func (coordinator *Coordinator) ConfirmHandover(eventID model.EventID, takeoverID string) (emergency.Evidence, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return emergency.Evidence{}, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Emergency.ConfirmHandover(event, takeoverID)
}

func (runtime *Runtime) EmergencyAssets() []emergency.Asset {
	return runtime.EmergencyState.Assets()
}
