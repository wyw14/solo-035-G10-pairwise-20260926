package emergency

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// Asset 是一张受控的紧急静态片素材。素材必须经过录入登记，
// 并声明允许使用的时段（ApplicableWindows，按周内本地时间表达），
// 只能在其适用时段内作为候选切入播出链路。
type Asset struct {
	ID                model.SlateID `json:"id"`
	Name              string        `json:"name"`
	ContentURI        string        `json:"content_uri"`
	Reason            string        `json:"reason"`
	ApplicableWindows []TimeWindow  `json:"applicable_windows"`
	Control           string        `json:"control"`
	RegisteredAt      time.Time     `json:"registered_at"`
	Retired           bool          `json:"retired"`
}

// TimeWindow 是静态片素材的适用时段：周几 + 当日起止时刻（分钟，0-1440）。
// Weekday 取 time.Weekday 的取值（0=周日 … 6=周六）。
type TimeWindow struct {
	Weekday  int `json:"weekday"`
	StartMin int `json:"start_min"`
	EndMin   int `json:"end_min"`
}

// Contains 判断某一时刻是否落在该适用时段内。
func (window TimeWindow) Contains(now time.Time) bool {
	minute := now.Hour()*60 + now.Minute()
	return window.Weekday == int(now.Weekday()) &&
		window.StartMin >= 0 && window.EndMin <= 24*60 &&
		window.StartMin < window.EndMin &&
		minute >= window.StartMin && minute < window.EndMin
}

func (window TimeWindow) valid() bool {
	return window.Weekday >= 0 && window.Weekday <= 6 &&
		window.StartMin >= 0 && window.EndMin <= 24*60 &&
		window.StartMin < window.EndMin
}

// Candidate 是在播出链路不可用时，系统给出的静态片候选。
type Candidate struct {
	Asset Asset  `json:"asset"`
	Due   string `json:"due_reason"`
}

// Proposal 是一次应急提案：针对某个事件、某个候选素材生成，
// 必须由导播用同一确认令牌再次确认后才会真正切入。
type Proposal struct {
	Token       model.CommandID `json:"token"`
	EventID     model.EventID   `json:"event_id"`
	AssetID     model.SlateID   `json:"asset_id"`
	Reason      string          `json:"reason"`
	ProposedAt  time.Time       `json:"proposed_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	ConfirmedAt *time.Time      `json:"confirmed_at,omitempty"`
}

// Evidence 是静态片切入、撤下或交接的实际播出证据。
type Evidence struct {
	ID         string          `json:"id"`
	EventID    model.EventID   `json:"event_id"`
	Kind       string          `json:"kind"`
	AssetID    model.SlateID   `json:"asset_id"`
	AssetName  string          `json:"asset_name"`
	Token      model.CommandID `json:"token,omitempty"`
	TakeoverID string          `json:"takeover_id,omitempty"`
	Detail     string          `json:"detail"`
	RecordedAt time.Time       `json:"recorded_at"`
}

// Takeover 是应急结束时、由导播选择并准备好的接管输出。
// 必须先 Prepare（就绪校验）再由导播明确 Confirm 才会完成交接。
type Takeover struct {
	ID          string           `json:"id"`
	EventID     model.EventID    `json:"event_id"`
	Label       string           `json:"label"`
	Source      model.SourceID   `json:"source"`
	Revision    model.RevisionID `json:"revision"`
	Fence       uint64           `json:"fence"`
	PreparedAt  time.Time        `json:"prepared_at"`
	ConfirmedAt *time.Time       `json:"confirmed_at,omitempty"`
	Status      string           `json:"status"`
}

// Session 描述一个事件当前的紧急静态片播出状态。
type Session struct {
	EventID     model.EventID `json:"event_id"`
	ActiveAsset *Asset        `json:"active_asset,omitempty"`
	LastAsset   *Asset        `json:"last_asset,omitempty"`
	TakenAt     *time.Time    `json:"taken_at,omitempty"`
	Proposal    *Proposal     `json:"proposal,omitempty"`
	Status      string        `json:"status"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// State 是紧急静态片通路的内存状态（证据另由 journal 持久化）。
type State struct {
	mu        sync.RWMutex
	assets    map[model.SlateID]Asset
	sessions  map[model.EventID]Session
	evidence  map[model.EventID][]Evidence
	takeovers map[string]Takeover
}

func NewState() *State {
	return &State{
		assets:    make(map[model.SlateID]Asset),
		sessions:  make(map[model.EventID]Session),
		evidence:  make(map[model.EventID][]Evidence),
		takeovers: make(map[string]Takeover),
	}
}

func (asset Asset) validate() error {
	if asset.ID == "" {
		return fmt.Errorf("slate asset identity is required")
	}
	if asset.Name == "" {
		return fmt.Errorf("slate asset name is required")
	}
	if asset.ContentURI == "" {
		return fmt.Errorf("slate asset content uri is required")
	}
	if asset.Control == "" {
		return fmt.Errorf("slate asset control owner is required")
	}
	if len(asset.ApplicableWindows) == 0 {
		return fmt.Errorf("slate asset %s must declare at least one applicable window", asset.ID)
	}
	for _, window := range asset.ApplicableWindows {
		if !window.valid() {
			return fmt.Errorf("slate asset %s has an invalid applicable window", asset.ID)
		}
	}
	return nil
}

func (state *State) Register(asset Asset) error {
	if err := asset.validate(); err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, exists := state.assets[asset.ID]; exists {
		return fmt.Errorf("slate asset %s is already registered", asset.ID)
	}
	state.assets[asset.ID] = asset
	return nil
}

func (state *State) RetireAsset(id model.SlateID) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	asset, ok := state.assets[id]
	if !ok || asset.Retired {
		return false
	}
	asset.Retired = true
	state.assets[id] = asset
	return true
}

func (state *State) Asset(id model.SlateID) (Asset, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	asset, ok := state.assets[id]
	return asset, ok
}

func (state *State) Assets() []Asset {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]Asset, 0, len(state.assets))
	for _, asset := range state.assets {
		items = append(items, asset)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RegisteredAt.Before(items[j].RegisteredAt) })
	return items
}

func (state *State) PutSession(session Session) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.sessions[session.EventID] = session
}

func (state *State) Session(eventID model.EventID) (Session, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	session, ok := state.sessions[eventID]
	return session, ok
}

func (state *State) Sessions() []Session {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]Session, 0, len(state.sessions))
	for _, session := range state.sessions {
		items = append(items, session)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].EventID < items[j].EventID })
	return items
}

func (state *State) AppendEvidence(evidence Evidence) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.evidence[evidence.EventID] = append(state.evidence[evidence.EventID], evidence)
}

func (state *State) Evidence(eventID model.EventID) []Evidence {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := append([]Evidence(nil), state.evidence[eventID]...)
	sort.Slice(items, func(i, j int) bool { return items[i].RecordedAt.Before(items[j].RecordedAt) })
	return items
}

func (state *State) PutTakeover(takeover Takeover) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.takeovers[takeover.ID] = takeover
}

func (state *State) Takeover(id string) (Takeover, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	takeover, ok := state.takeovers[id]
	return takeover, ok
}

// PreparedTakeover 返回某事件当前处于 prepared、等待导播确认的接管输出。
func (state *State) PreparedTakeover(eventID model.EventID) (Takeover, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	var found Takeover
	for _, takeover := range state.takeovers {
		if takeover.EventID == eventID && takeover.Status == "prepared" {
			if takeover.PreparedAt.After(found.PreparedAt) {
				found = takeover
			}
		}
	}
	return found, found.ID != ""
}
