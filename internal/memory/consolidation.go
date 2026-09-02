package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// consolidationArchiveBelow is the effective-score threshold under which an
// entry is archived (forgotten) during consolidation. Recency protects fresh
// entries; old low-importance episodes decay below the line naturally.
const consolidationArchiveBelow = 0.5

// profileKey hosts the consolidated user profile entry.
const profileKey = "user_profile"

// ConsolidationResult reports what one consolidation pass changed.
type ConsolidationResult struct {
	ArchivedCount  int      `json:"archived_count"`
	ProfileUpdated bool     `json:"profile_updated"`
	NewFactKeys    []string `json:"new_fact_keys,omitempty"`
	LLMUsed        bool     `json:"llm_used"`
	Skipped        string   `json:"skipped,omitempty"`
}

// Consolidate runs one memory-lifecycle pass for a user:
//
//  1. Forgetting — entries whose effective score (importance + recency +
//     access, no query signal) fell below consolidationArchiveBelow are
//     archived (excluded from retrieval and default listing, never deleted).
//  2. Consolidation — when enough active material exists and an LLM is
//     available, the entries are distilled into a user profile, obsolete
//     entries are archived, and episode content is precipitated into new
//     durable facts. With no model (or too little material) a degraded
//     rule-based profile is written instead, so the flow stays demoable in
//     mock mode.
func (s *Service) Consolidate(ctx context.Context, userID string) (*ConsolidationResult, error) {
	entries, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := &ConsolidationResult{NewFactKeys: []string{}}
	now := time.Now().Format(time.RFC3339)

	// --- 1. Forgetting: archive stale low-value entries ---
	var active []MemoryEntry
	for _, e := range entries {
		if e.Archived || e.Key == profileKey {
			active = append(active, e)
			continue
		}
		if scoreEntry(e, nil, false) < consolidationArchiveBelow {
			e.Archived = true
			e.UpdatedAt = now
			_ = s.store.Put(ctx, e)
			result.ArchivedCount++
			continue
		}
		active = append(active, e)
	}

	// --- 2. Consolidation ---
	if len(active) >= s.consolidateThreshold && s.chatModel != nil {
		if err := s.consolidateWithLLM(ctx, userID, active, result); err != nil {
			// Fall through to the degraded profile so the pass still helps.
			result.Skipped = fmt.Sprintf("LLM consolidation failed: %v", err)
		}
	} else if len(active) < s.consolidateThreshold {
		result.Skipped = fmt.Sprintf("only %d active entries (threshold %d) — profile rebuilt from entries, LLM consolidation skipped", len(active), s.consolidateThreshold)
	}

	if !result.ProfileUpdated {
		s.writeDegradedProfile(ctx, userID, active, result)
	}
	return result, nil
}

// consolidateWithLLM asks the model to distill the active entries into a
// profile, obsolete keys, and newly precipitated facts.
func (s *Service) consolidateWithLLM(ctx context.Context, userID string, active []MemoryEntry, result *ConsolidationResult) error {
	type llmEntry struct {
		Key        string `json:"key"`
		Value      string `json:"value"`
		Type       string `json:"type"`
		Importance int    `json:"importance"`
		UpdatedAt  string `json:"updated_at"`
	}
	items := make([]llmEntry, 0, len(active))
	for _, e := range active {
		if e.EffectiveType() == MemoryTypeEpisode && e.AccessCount == 0 {
			// Unreferenced episodes are consolidation fuel, but cap noise.
			continue
		}
		items = append(items, llmEntry{Key: e.Key, Value: e.Value, Type: e.EffectiveType(), Importance: e.EffectiveImportance(), UpdatedAt: e.UpdatedAt})
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return err
	}

	prompt := `你是用户记忆整合助手。以下是一位用户的长期记忆条目（JSON 数组）。请：
1. 生成一段简洁的用户画像 profile（100 字以内，第三人称）。
2. 找出过时、被覆盖或无价值的条目 key 列表 obsolete_keys（没有则空数组）。
3. 从情景/碎片记录中沉淀新的持久事实 new_facts（{key,value,type,importance}，没有则空数组）。
只返回 JSON 对象：{"profile":"...","obsolete_keys":[],"new_facts":[]}

条目：` + string(payload)

	resp, err := s.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("你是用户记忆整合助手，只返回 JSON 对象，不返回其他内容。"),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return err
	}

	content := strings.TrimSpace(resp.Content)
	// Extract the JSON object (may be wrapped in markdown fences).
	start := strings.Index(content, "{")
	if start == -1 {
		return fmt.Errorf("LLM did not return a JSON object")
	}
	depth := 0
	end := -1
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end != -1 {
			break
		}
	}
	if end == -1 {
		return fmt.Errorf("LLM JSON object not closed")
	}

	var parsed struct {
		Profile      string   `json:"profile"`
		ObsoleteKeys []string `json:"obsolete_keys"`
		NewFacts     []struct {
			Key        string `json:"key"`
			Value      string `json:"value"`
			Type       string `json:"type"`
			Importance int    `json:"importance"`
		} `json:"new_facts"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &parsed); err != nil {
		return fmt.Errorf("LLM consolidation JSON parse failed: %w", err)
	}

	now := time.Now().Format(time.RFC3339)
	for _, k := range parsed.ObsoleteKeys {
		if k == "" || k == profileKey {
			continue
		}
		if e, ok, _ := s.store.Get(ctx, userID, k); ok && !e.Archived {
			e.Archived = true
			e.UpdatedAt = now
			_ = s.store.Put(ctx, e)
			result.ArchivedCount++
		}
	}
	for _, f := range parsed.NewFacts {
		if f.Key == "" || f.Value == "" {
			continue
		}
		if err := s.UpsertPreference(ctx, userID, f.Key, f.Value, EntryMeta{
			Type: f.Type, Importance: f.Importance, Source: "consolidated",
		}); err == nil {
			result.NewFactKeys = append(result.NewFactKeys, f.Key)
		}
	}
	if parsed.Profile != "" {
		if err := s.UpsertPreference(ctx, userID, profileKey, parsed.Profile, EntryMeta{
			Type: MemoryTypeIdentity, Importance: MaxImportance, Source: "consolidated",
		}); err == nil {
			result.ProfileUpdated = true
			result.LLMUsed = true
		}
	}
	return nil
}

// writeDegradedProfile builds a simple profile by concatenating the active
// preference/identity entries — the no-LLM fallback path.
func (s *Service) writeDegradedProfile(ctx context.Context, userID string, active []MemoryEntry, result *ConsolidationResult) {
	var lines []string
	for _, e := range active {
		switch e.EffectiveType() {
		case MemoryTypePreference, MemoryTypeIdentity, MemoryTypeRule:
			lines = append(lines, fmt.Sprintf("%s: %s", e.Key, e.Value))
		}
	}
	if len(lines) == 0 {
		return
	}
	profile := "用户画像（规则汇总）：" + strings.Join(lines, "；")
	if err := s.UpsertPreference(ctx, userID, profileKey, profile, EntryMeta{
		Type: MemoryTypeIdentity, Importance: MaxImportance, Source: "consolidated",
	}); err == nil {
		result.ProfileUpdated = true
	}
}
