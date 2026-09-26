package emergency

import "fmt"

// AssetsList 导出全部已登记素材（含已停用），用于检查点。
func (state *State) AssetsList() []Asset {
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]Asset, 0, len(state.assets))
	for _, asset := range state.assets {
		items = append(items, asset)
	}
	return items
}

// StateRestoreAssets 恢复受控素材登记。
func (state *State) StateRestoreAssets(assets []Asset) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, asset := range assets {
		if err := asset.validate(); err != nil {
			return fmt.Errorf("restore slate asset %s: %w", asset.ID, err)
		}
		state.assets[asset.ID] = asset
	}
	return nil
}

// RestoreSessions 恢复应急会话。
func (state *State) RestoreSessions(sessions []Session) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, session := range sessions {
		if !session.EventID.Valid() {
			return fmt.Errorf("emergency session has invalid event")
		}
		state.sessions[session.EventID] = session
	}
	return nil
}
