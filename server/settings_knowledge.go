package server

// knowledge_inject_mode controls whether the project-knowledge block reaches
// planner/worker prompts:
//
//	off      no selection, no injection
//	shadow   select, render and record what would have been shown (log-only)
//	inject   select, record, and append the block to the round input (default)
//
// Read per planner round / worker launch, so a change applies to the next round
// without rebuilding the agent.

import (
	"fmt"
	"strings"
)

const settingKnowledgeInjectMode = "knowledge_inject_mode"

// KnowledgeInjectMode returns the knowledge injection mode (default "inject"
// when unset or set to something unrecognised).
func (m *Manager) KnowledgeInjectMode() string {
	v, ok, err := m.pg.GetSetting(settingKnowledgeInjectMode)
	if err != nil || !ok {
		return "inject"
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off":
		return "off"
	case "shadow":
		return "shadow"
	default:
		return "inject"
	}
}

// SetKnowledgeInjectMode persists the knowledge injection mode.
func (m *Manager) SetKnowledgeInjectMode(mode string) error {
	switch mode {
	case "off", "shadow", "inject":
	default:
		return fmt.Errorf("knowledge inject mode must be off, shadow or inject")
	}
	return m.pg.SetSetting(settingKnowledgeInjectMode, mode)
}
