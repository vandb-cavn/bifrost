package governance

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateQuotaWindow_RollingEnded(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	lastReset := now.Add(-2 * time.Hour)
	res, err := EvaluateQuotaWindow(now, "1h", lastReset, false)
	require.NoError(t, err)
	assert.True(t, res.PeriodEnded)
	assert.Equal(t, now, res.NewLastReset)
}

func TestEvaluateQuotaWindow_RollingNotEnded(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	lastReset := now.Add(-30 * time.Minute)
	res, err := EvaluateQuotaWindow(now, "1h", lastReset, false)
	require.NoError(t, err)
	assert.False(t, res.PeriodEnded)
}

func TestEvaluateQuotaWindow_CalendarMonthBoundary(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 30, 0, 0, time.UTC)
	lastReset := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	res, err := EvaluateQuotaWindow(now, "1M", lastReset, true)
	require.NoError(t, err)
	assert.True(t, res.PeriodEnded)
	assert.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), res.NewLastReset)
}

func TestEvaluateQuotaWindow_CalendarFlagNonAlignableDurationUsesRolling(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	lastReset := now.Add(-30 * time.Minute)
	res, err := EvaluateQuotaWindow(now, "1h", lastReset, true)
	require.NoError(t, err)
	assert.False(t, res.PeriodEnded)
}

func TestEvaluateQuotaWindow_EmptyDuration(t *testing.T) {
	res, err := EvaluateQuotaWindow(time.Now(), "", time.Now(), false)
	require.NoError(t, err)
	assert.False(t, res.PeriodEnded)
}

func TestEvaluateQuotaWindow_InvalidDuration(t *testing.T) {
	_, err := EvaluateQuotaWindow(time.Now(), "not-a-duration", time.Now(), false)
	require.Error(t, err)
}

func TestCheckBudget_CalendarAligned_GraceSkipWhenPeriodEnded(t *testing.T) {
	now := time.Now().UTC()
	periodStart := configstoreTables.GetCalendarPeriodStart("1M", now)
	lastReset := periodStart.AddDate(0, 0, -3)

	monthDur, err := configstoreTables.ParseDuration("1M")
	require.NoError(t, err)
	if !periodStart.After(lastReset) {
		t.Fatalf("test setup: expected calendar period ended")
	}
	if now.Sub(lastReset) >= monthDur {
		t.Skip("current date does not separate calendar grace from rolling expiry")
	}

	budget := buildBudgetWithUsage("cal-budget", 1.0, 999.0, "1M")
	budget.IsCalendarAligned = true
	budget.LastReset = lastReset

	store, err := NewLocalGovernanceStore(context.Background(), NewMockLogger(), nil, &configstore.GovernanceConfig{
		Budgets: []configstoreTables.TableBudget{*budget},
	}, nil)
	require.NoError(t, err)

	decision, err := store.CheckBudget(context.Background(), EntityWiseBudgets{
		"test": {budget},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, DecisionAllow, decision)
}
