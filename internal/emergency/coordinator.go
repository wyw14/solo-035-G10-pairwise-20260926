package emergency

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/solo-035/internal/journal"
	"github.com/wyw14/solo-035/internal/model"
)

const (
	// StatusHolding 表示静态片正在播出。
	StatusHolding = "holding-slate"
	// StatusProposed 表示已给出候选、等待导播明确确认。
	StatusProposed = "proposed"
	// StatusWithdrawn 表示静态片已撤下、尚未交接。
	StatusWithdrawn = "withdrawn"
	// StatusHandedOver 表示已交接给接管输出、应急结束。
	StatusHandedOver = "handed-over"

	// EvidenceKindTake 静态片切入证据。
	EvidenceKindTake = "slate-take"
	// EvidenceKindWithdraw 静态片撤下证据。
	EvidenceKindWithdraw = "slate-withdraw"
	// EvidenceKindHandover 静态片交接给接管输出的证据。
	EvidenceKindHandover = "slate-handover"

	proposalTTL = 60 * time.Second
)

// TakeoverOutput 描述一路已经准备好、可以接管静态片的正常播出输出。
// 由上层（playout 链路）在准备接管时提供。
type TakeoverOutput struct {
	Source   model.SourceID
	Revision model.RevisionID
	Fence    uint64
}

// RegisterInput 是受控静态片素材的录入内容。
type RegisterInput struct {
	Name              string
	ContentURI        string
	Reason            string
	ApplicableWindows []TimeWindow
	Control           string
}

type Coordinator struct {
	state     *State
	journal   *journal.Integration
	clock     func() time.Time
	readiness func(model.EventID) (TakeoverOutput, bool)
	handover  func(model.LiveEvent, Takeover) error
}

func NewCoordinator(
	state *State,
	journalIntegration *journal.Integration,
	clock func() time.Time,
	readiness func(model.EventID) (TakeoverOutput, bool),
	handover func(model.LiveEvent, Takeover) error,
) *Coordinator {
	return &Coordinator{state: state, journal: journalIntegration, clock: clock, readiness: readiness, handover: handover}
}

// RegisterAsset 录入一张受控静态片素材及其适用时段。
func (coordinator *Coordinator) RegisterAsset(input RegisterInput) (Asset, error) {
	asset := Asset{
		ID:                model.NewSlateID(),
		Name:              input.Name,
		ContentURI:        input.ContentURI,
		Reason:            input.Reason,
		ApplicableWindows: input.ApplicableWindows,
		Control:           input.Control,
		RegisteredAt:      coordinator.clock().UTC(),
	}
	if err := coordinator.state.Register(asset); err != nil {
		return Asset{}, err
	}
	return asset, nil
}

func (coordinator *Coordinator) RetireAsset(id model.SlateID) error {
	if !coordinator.state.RetireAsset(id) {
		return fmt.Errorf("slate asset %s does not exist or is already retired", id)
	}
	return nil
}

// Candidates 返回当前时刻处于适用时段内的受控素材候选。
// 优先返回带更窄时段限制的素材，全天候兜底素材排在最后。
func (coordinator *Coordinator) Candidates(now time.Time) []Candidate {
	now = now.UTC()
	candidates := make([]Candidate, 0)
	for _, asset := range coordinator.state.Assets() {
		if asset.Retired {
			continue
		}
		for _, window := range asset.ApplicableWindows {
			if window.Contains(now) {
				candidates = append(candidates, Candidate{Asset: asset, Due: describeWindow(window)})
				break
			}
		}
	}
	sortCandidates(candidates)
	return candidates
}

// Propose 针对事件生成静态片切入提案（候选 + 确认令牌），不直接切入。
func (coordinator *Coordinator) Propose(event model.LiveEvent, assetID model.SlateID, reason string) (Proposal, error) {
	if !event.ID.Valid() {
		return Proposal{}, fmt.Errorf("emergency proposal requires a valid event")
	}
	if session, ok := coordinator.state.Session(event.ID); ok && session.ActiveAsset != nil {
		return Proposal{}, fmt.Errorf("event %s already holds slate %s; withdraw it before another take", event.ID, session.ActiveAsset.ID)
	}
	asset, ok := coordinator.state.Asset(assetID)
	if !ok {
		return Proposal{}, fmt.Errorf("slate asset %s is not registered", assetID)
	}
	if asset.Retired {
		return Proposal{}, fmt.Errorf("slate asset %s has been retired", assetID)
	}
	now := coordinator.clock().UTC()
	matched := false
	for _, window := range asset.ApplicableWindows {
		if window.Contains(now) {
			matched = true
			break
		}
	}
	if !matched {
		return Proposal{}, fmt.Errorf("slate asset %s is outside its applicable window", assetID)
	}
	if reason == "" {
		reason = "playout chain unavailable"
	}
	proposal := Proposal{
		Token:      model.NewCommandID(),
		EventID:    event.ID,
		AssetID:    asset.ID,
		Reason:     reason,
		ProposedAt: now,
		ExpiresAt:  now.Add(proposalTTL),
	}
	coordinator.state.PutSession(Session{EventID: event.ID, Proposal: &proposal, Status: StatusProposed, UpdatedAt: now})
	return proposal, nil
}

// ConfirmTake 凭确认令牌执行切入；令牌必须与待确认提案一致且未过期。
func (coordinator *Coordinator) ConfirmTake(event model.LiveEvent, token model.CommandID) (Evidence, error) {
	now := coordinator.clock().UTC()
	session, ok := coordinator.state.Session(event.ID)
	if !ok || session.Proposal == nil {
		return Evidence{}, fmt.Errorf("no pending slate proposal for event %s", event.ID)
	}
	proposal := session.Proposal
	if proposal.Token != token {
		return Evidence{}, fmt.Errorf("slate confirmation token does not match the pending proposal")
	}
	if !token.Valid() {
		return Evidence{}, fmt.Errorf("slate confirmation token is invalid")
	}
	if now.After(proposal.ExpiresAt) {
		return Evidence{}, fmt.Errorf("slate proposal %s expired at %s; propose again", token, proposal.ExpiresAt)
	}
	asset, ok := coordinator.state.Asset(proposal.AssetID)
	if !ok || asset.Retired {
		return Evidence{}, fmt.Errorf("proposed slate asset %s is no longer available", proposal.AssetID)
	}
	takenAt := now
	session.ActiveAsset = &asset
	session.TakenAt = &takenAt
	session.Proposal = nil
	session.Status = StatusHolding
	session.UpdatedAt = now
	coordinator.state.PutSession(session)
	evidence := coordinator.newEvidence(event.ID, EvidenceKindTake, asset, token, "", proposal.Reason, now)
	return coordinator.persist(event, evidence)
}

// Withdraw 撤下正在播出的静态片。撤下后屏幕不再由静态片承担，
// 必须再选择接管输出完成交接，应急才算结束。
func (coordinator *Coordinator) Withdraw(event model.LiveEvent, reason string) (Evidence, error) {
	now := coordinator.clock().UTC()
	session, ok := coordinator.state.Session(event.ID)
	if !ok || session.ActiveAsset == nil {
		return Evidence{}, fmt.Errorf("event %s has no active slate to withdraw", event.ID)
	}
	asset := *session.ActiveAsset
	if reason == "" {
		reason = "director withdrew emergency slate"
	}
	withdrawn := asset
	session.ActiveAsset = nil
	session.TakenAt = nil
	session.LastAsset = &withdrawn
	session.Proposal = nil
	session.Status = StatusWithdrawn
	session.UpdatedAt = now
	coordinator.state.PutSession(session)
	evidence := coordinator.newEvidence(event.ID, EvidenceKindWithdraw, asset, "", "", reason, now)
	return coordinator.persist(event, evidence)
}

// PrepareTakeover 在静态片撤下后，由导播选择一路接管输出并校验其已准备好
// （在播链路已恢复出有效输出）。准备好后仍需 ConfirmHandover 明确确认。
func (coordinator *Coordinator) PrepareTakeover(event model.LiveEvent, label string) (Takeover, error) {
	session, ok := coordinator.state.Session(event.ID)
	if !ok || session.LastAsset == nil {
		return Takeover{}, fmt.Errorf("event %s has no withdrawn slate to hand over", event.ID)
	}
	if session.Status == StatusHandedOver {
		return Takeover{}, fmt.Errorf("event %s emergency has already ended", event.ID)
	}
	if coordinator.readiness == nil {
		return Takeover{}, fmt.Errorf("takeover readiness probe is not configured")
	}
	output, ready := coordinator.readiness(event.ID)
	if !ready || !output.Source.Valid() || !output.Revision.Valid() {
		return Takeover{}, fmt.Errorf("takeover output for event %s is not ready", event.ID)
	}
	if label == "" {
		label = "restored-program"
	}
	takeover := Takeover{
		ID:         uuid.NewString(),
		EventID:    event.ID,
		Label:      label,
		Source:     output.Source,
		Revision:   output.Revision,
		Fence:      output.Fence,
		PreparedAt: coordinator.clock().UTC(),
		Status:     "prepared",
	}
	coordinator.state.PutTakeover(takeover)
	session.Status = StatusWithdrawn + "-awaiting-handover"
	session.UpdatedAt = takeover.PreparedAt
	coordinator.state.PutSession(session)
	return takeover, nil
}

// ConfirmHandover 由导播明确确认接管输出，完成交接并结束应急。
func (coordinator *Coordinator) ConfirmHandover(event model.LiveEvent, takeoverID string) (Evidence, error) {
	takeover, ok := coordinator.state.Takeover(takeoverID)
	if !ok {
		return Evidence{}, fmt.Errorf("takeover %s is not prepared", takeoverID)
	}
	if takeover.EventID != event.ID {
		return Evidence{}, fmt.Errorf("takeover %s belongs to another event", takeoverID)
	}
	if takeover.Status != "prepared" {
		return Evidence{}, fmt.Errorf("takeover %s has already been confirmed", takeoverID)
	}
	session, ok := coordinator.state.Session(event.ID)
	if !ok || session.ActiveAsset != nil || session.LastAsset == nil {
		return Evidence{}, fmt.Errorf("event %s slate must be withdrawn before hand over", event.ID)
	}
	asset := *session.LastAsset
	if coordinator.handover == nil {
		return Evidence{}, fmt.Errorf("takeover handover action is not configured")
	}
	if err := coordinator.handover(event, takeover); err != nil {
		return Evidence{}, fmt.Errorf("takeover output failed to go on air: %w", err)
	}
	now2 := coordinator.clock().UTC()
	takeover.ConfirmedAt = &now2
	takeover.Status = "confirmed"
	coordinator.state.PutTakeover(takeover)
	session.LastAsset = nil
	session.Status = StatusHandedOver
	session.UpdatedAt = now2
	coordinator.state.PutSession(session)
	evidence := coordinator.newEvidence(event.ID, EvidenceKindHandover, asset, "", takeover.ID,
		fmt.Sprintf("handed over to %s (fence %d)", takeover.Label, takeover.Fence), now2)
	return coordinator.persist(event, evidence)
}

func (coordinator *Coordinator) newEvidence(eventID model.EventID, kind string, asset Asset, token model.CommandID, takeoverID string, detail string, now time.Time) Evidence {
	return Evidence{
		ID:         uuid.NewString(),
		EventID:    eventID,
		Kind:       kind,
		AssetID:    asset.ID,
		AssetName:  asset.Name,
		Token:      token,
		TakeoverID: takeoverID,
		Detail:     detail,
		RecordedAt: now.UTC(),
	}
}

// persist 将切入/撤下/交接证据写入日志，落盘成功后才在内存中留存，
// 保证每一次实际播出动作都有可追溯证据。
func (coordinator *Coordinator) persist(event model.LiveEvent, evidence Evidence) (Evidence, error) {
	if _, err := coordinator.journal.Persist(event.ID, evidence.Kind, event.Revision, 0, evidence); err != nil {
		return Evidence{}, err
	}
	coordinator.state.AppendEvidence(evidence)
	return evidence, nil
}

func describeWindow(window TimeWindow) string {
	weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	return fmt.Sprintf("%s %02d:%02d-%02d:%02d 适用", weekdays[window.Weekday],
		window.StartMin/60, window.StartMin%60, window.EndMin/60, window.EndMin%60)
}

func sortCandidates(candidates []Candidate) {
	// 全天候窗口（覆盖整周 7×24h）优先级最低，作为兜底排在最后；其余按录入顺序保留。
	sort.SliceStable(candidates, func(i, j int) bool {
		return !isAllDayFallback(candidates[i].Asset) && isAllDayFallback(candidates[j].Asset)
	})
}

func isAllDayFallback(asset Asset) bool {
	if len(asset.ApplicableWindows) != 7 {
		return false
	}
	for _, window := range asset.ApplicableWindows {
		if window.StartMin != 0 || window.EndMin != 24*60 {
			return false
		}
	}
	return true
}
