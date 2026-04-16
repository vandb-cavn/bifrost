package configstore

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// ---------- Guardrail Rules ----------

func (s *RDBConfigStore) GetGuardrailRules(ctx context.Context) ([]*tables.TableGuardrailRule, error) {
	var rules []*tables.TableGuardrailRule
	if err := s.db.WithContext(ctx).
		Preload("Profiles").
		Order("priority ASC, created_at DESC").
		Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

func (s *RDBConfigStore) GetGuardrailRuleByID(ctx context.Context, id string) (*tables.TableGuardrailRule, error) {
	var rule tables.TableGuardrailRule
	if err := s.db.WithContext(ctx).
		Preload("Profiles").
		Where("id = ?", id).
		First(&rule).Error; err != nil {
		return nil, fmt.Errorf("guardrail rule %q not found: %w", id, err)
	}
	return &rule, nil
}

func (s *RDBConfigStore) CreateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule) error {
	return s.db.WithContext(ctx).Create(rule).Error
}

func (s *RDBConfigStore) UpdateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule) error {
	return s.db.WithContext(ctx).Save(rule).Error
}

func (s *RDBConfigStore) DeleteGuardrailRule(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&tables.TableGuardrailRule{}).Error
}

// ---------- Guardrail Profiles ----------

func (s *RDBConfigStore) GetGuardrailProfiles(ctx context.Context) ([]*tables.TableGuardrailProfile, error) {
	var profiles []*tables.TableGuardrailProfile
	if err := s.db.WithContext(ctx).
		Order("created_at DESC").
		Find(&profiles).Error; err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *RDBConfigStore) GetGuardrailProfileByID(ctx context.Context, id string) (*tables.TableGuardrailProfile, error) {
	var profile tables.TableGuardrailProfile
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&profile).Error; err != nil {
		return nil, fmt.Errorf("guardrail profile %q not found: %w", id, err)
	}
	return &profile, nil
}

func (s *RDBConfigStore) CreateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile) error {
	return s.db.WithContext(ctx).Create(profile).Error
}

func (s *RDBConfigStore) UpdateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile) error {
	return s.db.WithContext(ctx).Save(profile).Error
}

func (s *RDBConfigStore) DeleteGuardrailProfile(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&tables.TableGuardrailProfile{}).Error
}

// ---------- Link / Unlink ----------

func (s *RDBConfigStore) LinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error {
	rule, err := s.GetGuardrailRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}
	profile, err := s.GetGuardrailProfileByID(ctx, profileID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(rule).Association("Profiles").Append(profile)
}

func (s *RDBConfigStore) UnlinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error {
	rule, err := s.GetGuardrailRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}
	profile, err := s.GetGuardrailProfileByID(ctx, profileID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(rule).Association("Profiles").Delete(profile)
}
