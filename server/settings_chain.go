package server

// Setting wrapper for the correlation (chain) agent. It is on unless the operator
// turns it off: a correlation pass is cheap next to the exploration it reads.

const settingChainAgentEnabled = "chain_agent_enabled"

// ChainAgentEnabled reports whether the chain agent runs for new tasks.
func (m *Manager) ChainAgentEnabled() bool {
	v, ok, err := m.pg.GetSetting(settingChainAgentEnabled)
	if err != nil || !ok {
		return true
	}
	return v != "0" && v != "false" && v != "off"
}

// SetChainAgentEnabled stores the switch.
func (m *Manager) SetChainAgentEnabled(on bool) error {
	return m.pg.SetSetting(settingChainAgentEnabled, map[bool]string{true: "1", false: "0"}[on])
}
