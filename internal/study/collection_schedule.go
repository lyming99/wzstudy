package study

import (
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA zones available in minimal images and Windows packages.
)

// CollectionSchedule deliberately supports common rules, not arbitrary cron/scripts.
// Weekdays use Go/JavaScript numbering: Sunday=0, Saturday=6.
type CollectionSchedule struct {
	Kind            string                     `json:"kind"`
	Timezone        string                     `json:"timezone"`
	At              *time.Time                 `json:"at,omitempty"`
	IntervalMinutes int                        `json:"interval_minutes,omitempty"`
	TimeOfDay       string                     `json:"time_of_day,omitempty"`
	Weekdays        []int                      `json:"weekdays,omitempty"`
	UnreadCondition *CollectionUnreadCondition `json:"unread_condition,omitempty"`
}

// A nil threshold is different from an explicit zero (for empty-inventory rules).
type CollectionUnreadCondition struct {
	Operator  string `json:"operator"`
	Threshold *int   `json:"threshold"`
}

func normalizeCollectionSchedule(in CollectionSchedule) (CollectionSchedule, error) {
	in.Kind = strings.TrimSpace(in.Kind)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if len(in.Timezone) > 100 || in.Timezone == "Local" {
		return in, ErrInvalid
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return in, ErrInvalid
	}
	if in.Kind != "unread_inventory" && in.UnreadCondition != nil {
		return in, ErrInvalid
	}
	switch in.Kind {
	case "unread_inventory":
		if in.UnreadCondition == nil || in.UnreadCondition.Threshold == nil || *in.UnreadCondition.Threshold < 0 || *in.UnreadCondition.Threshold > 1000000 || in.IntervalMinutes < 1 || in.IntervalMinutes > 10080 || in.At != nil || in.TimeOfDay != "" || len(in.Weekdays) != 0 {
			return in, ErrInvalid
		}
		switch in.UnreadCondition.Operator {
		case "lt", "lte", "eq", "gte", "gt":
		default:
			return in, ErrInvalid
		}
	case "manual", "webhook":
		if in.At != nil || in.IntervalMinutes != 0 || in.TimeOfDay != "" || len(in.Weekdays) != 0 {
			return in, ErrInvalid
		}
	case "once":
		if in.At == nil || in.At.IsZero() || in.At.Year() < 1 || in.At.Year() > 9999 || in.IntervalMinutes != 0 || in.TimeOfDay != "" || len(in.Weekdays) != 0 {
			return in, ErrInvalid
		}
		at := in.At.UTC().Truncate(time.Second)
		in.At = &at
	case "interval":
		if in.IntervalMinutes < 1 || in.IntervalMinutes > 10080 || in.At != nil || in.TimeOfDay != "" || len(in.Weekdays) != 0 {
			return in, ErrInvalid
		}
	case "daily", "weekly":
		if in.At != nil || in.IntervalMinutes != 0 || len(in.TimeOfDay) != 5 {
			return in, ErrInvalid
		}
		if _, err := time.Parse("15:04", in.TimeOfDay); err != nil {
			return in, ErrInvalid
		}
		if in.Kind == "daily" && len(in.Weekdays) != 0 {
			return in, ErrInvalid
		}
		if in.Kind == "weekly" {
			if len(in.Weekdays) == 0 || len(in.Weekdays) > 7 {
				return in, ErrInvalid
			}
			seen := map[int]bool{}
			days := make([]int, 0, len(in.Weekdays))
			for _, day := range in.Weekdays {
				if day < 0 || day > 6 || seen[day] {
					return in, ErrInvalid
				}
				seen[day] = true
				days = append(days, day)
			}
			sort.Ints(days)
			in.Weekdays = days
		}
	default:
		return in, ErrInvalid
	}
	return in, nil
}

// nextCollectionTime is strictly after after. Nonexistent DST wall times are
// skipped; an ambiguous wall time maps to one Date value and runs only once.
func nextCollectionTime(in CollectionSchedule, after time.Time) *time.Time {
	after = after.UTC()
	switch in.Kind {
	case "once":
		if in.At != nil && in.At.After(after) {
			at := in.At.UTC()
			return &at
		}
	case "interval", "unread_inventory":
		at := after.Truncate(time.Second).Add(time.Duration(in.IntervalMinutes) * time.Minute)
		return &at
	case "daily", "weekly":
		loc, err := time.LoadLocation(in.Timezone)
		if err != nil {
			return nil
		}
		clock, err := time.Parse("15:04", in.TimeOfDay)
		if err != nil {
			return nil
		}
		local := after.In(loc)
		// Use noon to enumerate calendar dates rather than adding 24-hour days.
		base := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
		for offset := 0; offset <= 14; offset++ {
			day := base.AddDate(0, 0, offset)
			if in.Kind == "weekly" {
				found := false
				for _, d := range in.Weekdays {
					found = found || d == int(day.Weekday())
				}
				if !found {
					continue
				}
			}
			at := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
			if at.Day() != day.Day() || at.Month() != day.Month() || at.Hour() != clock.Hour() || at.Minute() != clock.Minute() {
				continue
			}
			if at.After(after) {
				utc := at.UTC()
				return &utc
			}
		}
	}
	return nil
}

func PreviewCollectionSchedule(in CollectionSchedule, after time.Time, count int) ([]time.Time, error) {
	in, err := normalizeCollectionSchedule(in)
	if err != nil || count < 1 || count > 10 {
		return nil, ErrInvalid
	}
	out := []time.Time{}
	for i := 0; i < count; i++ {
		next := nextCollectionTime(in, after)
		if next == nil {
			break
		}
		out = append(out, *next)
		after = *next
	}
	return out, nil
}

// Coalesce a missed recurring schedule into its most recent slot, retaining its
// original interval anchor. No unbounded catch-up loop after a long outage.
func latestCollectionSlot(in CollectionSchedule, first, now time.Time) time.Time {
	if in.Kind == "interval" || in.Kind == "unread_inventory" {
		step := time.Duration(in.IntervalMinutes) * time.Minute
		return first.Add((now.Sub(first) / step) * step)
	}
	if in.Kind == "daily" || in.Kind == "weekly" {
		cursor := first.Add(-time.Second)
		if cursor.Before(now.Add(-15 * 24 * time.Hour)) {
			cursor = now.Add(-15 * 24 * time.Hour)
		}
		last := first
		for i := 0; i < 32; i++ {
			next := nextCollectionTime(in, cursor)
			if next == nil || next.After(now) {
				break
			}
			last = *next
			cursor = *next
		}
		return last
	}
	return first
}
