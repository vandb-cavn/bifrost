package governance

import (
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// QuotaWindowResult is the outcome of evaluating a single counter's reset window.
type QuotaWindowResult struct {
	// PeriodEnded is true when the current time is past the window boundary
	// implied by lastReset (rolling) or the calendar period start (calendar-aligned).
	PeriodEnded bool
	// NewLastReset is the LastReset value to apply on reset (period start or now).
	NewLastReset time.Time
}

// EvaluateQuotaWindow decides whether a budget or rate-limit counter period has ended.
// resetDuration is budget.ResetDuration or *rl.TokenResetDuration / *rl.RequestResetDuration.
// calendarAligned comes from entity IsCalendarAligned (propagated from VK/team at load time).
func EvaluateQuotaWindow(
	now time.Time,
	resetDuration string,
	lastReset time.Time,
	calendarAligned bool,
) (QuotaWindowResult, error) {
	if resetDuration == "" {
		return QuotaWindowResult{}, nil
	}
	if calendarAligned && configstoreTables.IsCalendarAlignableDuration(resetDuration) {
		periodStart := configstoreTables.GetCalendarPeriodStart(resetDuration, now)
		if periodStart.After(lastReset) {
			return QuotaWindowResult{PeriodEnded: true, NewLastReset: periodStart}, nil
		}
		return QuotaWindowResult{}, nil
	}
	duration, err := configstoreTables.ParseDuration(resetDuration)
	if err != nil {
		return QuotaWindowResult{}, err
	}
	if now.Sub(lastReset) >= duration {
		return QuotaWindowResult{PeriodEnded: true, NewLastReset: now}, nil
	}
	return QuotaWindowResult{}, nil
}

// evaluateQuotaWindowPtr is like EvaluateQuotaWindow but returns nil *time.Time when no reset is due.
func evaluateQuotaWindowPtr(now time.Time, resetDuration *string, lastReset time.Time, calendarAligned bool) *time.Time {
	if resetDuration == nil {
		return nil
	}
	res, err := EvaluateQuotaWindow(now, *resetDuration, lastReset, calendarAligned)
	if err != nil || !res.PeriodEnded {
		return nil
	}
	t := res.NewLastReset
	return &t
}
