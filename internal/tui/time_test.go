package tui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatRelative(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"zero time", time.Time{}, "never"},
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"1 minute ago", now.Add(-90 * time.Second), "1 minute ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour ago", now.Add(-90 * time.Minute), "1 hour ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hours ago"},
		{"yesterday", now.Add(-30 * time.Hour), "yesterday"},
		{"3 days ago", now.Add(-3 * 24 * time.Hour), "3 days ago"},
		{"1 week ago", now.Add(-10 * 24 * time.Hour), "1 week ago"},
		{"2 weeks ago", now.Add(-17 * 24 * time.Hour), "2 weeks ago"},
		{"1 month ago", now.Add(-45 * 24 * time.Hour), "1 month ago"},
		{"3 months ago", now.Add(-100 * 24 * time.Hour), "3 months ago"},
		{"1 year ago", now.Add(-400 * 24 * time.Hour), "1 year ago"},
		{"2 years ago", now.Add(-800 * 24 * time.Hour), "2 years ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatRelative(tt.time))
		})
	}
}

func TestFormatRelative_Future(t *testing.T) {
	// Add a small buffer to account for test execution time
	buffer := 2 * time.Second
	now := time.Now()

	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"in a moment", now.Add(30*time.Second + buffer), "in a moment"},
		{"in 1 minute", now.Add(90*time.Second + buffer), "in 1 minute"},
		{"in 5 minutes", now.Add(5*time.Minute + buffer), "in 5 minutes"},
		{"in 1 hour", now.Add(90*time.Minute + buffer), "in 1 hour"},
		{"in 3 hours", now.Add(3*time.Hour + buffer), "in 3 hours"},
		{"in 2 days", now.Add(48*time.Hour + buffer), "in 2 days"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatRelative(tt.time))
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"zero", 0, "0s"},
		{"seconds", 30 * time.Second, "30s"},
		{"minutes and seconds", 2*time.Minute + 30*time.Second, "2m 30s"},
		{"minutes only", 5 * time.Minute, "5m"},
		{"hours and minutes", 2*time.Hour + 15*time.Minute, "2h 15m"},
		{"hours only", 3 * time.Hour, "3h"},
		{"negative", -5 * time.Minute, "-5m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatDuration(tt.duration))
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 14, 30, 45, 0, time.Local)
	yesterday := today.Add(-24 * time.Hour)

	tests := []struct {
		name  string
		time  time.Time
		short bool
		want  string
	}{
		{"zero time short", time.Time{}, true, "-"},
		{"zero time long", time.Time{}, false, "-"},
		{"today short", today, true, "14:30"},
		{"today long", today, false, "14:30:45"},
		{"yesterday short", yesterday, true, yesterday.Format("Jan 2 15:04")},
		{"yesterday long", yesterday, false, yesterday.Format("Jan 2 15:04:05")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatTimestamp(tt.time, tt.short))
		})
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"zero", 0, "00:00:00"},
		{"seconds only", 45 * time.Second, "00:00:45"},
		{"minutes and seconds", 5*time.Minute + 30*time.Second, "00:05:30"},
		{"hours minutes seconds", 2*time.Hour + 15*time.Minute + 30*time.Second, "02:15:30"},
		{"over 99 hours", 100*time.Hour + 30*time.Minute, "4d 04:30:00"},
		{"negative becomes zero", -5 * time.Minute, "00:00:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatUptime(tt.duration))
		})
	}
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"zero time", time.Time{}, "-"},
		{"normal date", time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), "Mar 15, 2024"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatDate(tt.time))
		})
	}
}

func TestFormatDateTime(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"zero time", time.Time{}, "-"},
		{"normal datetime", time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC), "Mar 15, 2024 14:30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatDateTime(tt.time))
		})
	}
}

func TestParseDurationOrDefault(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		defaultVal time.Duration
		want       time.Duration
	}{
		{"valid duration", "5m", time.Minute, 5 * time.Minute},
		{"empty string", "", time.Minute, time.Minute},
		{"invalid string", "invalid", time.Minute, time.Minute},
		{"zero duration", "0s", time.Minute, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseDurationOrDefault(tt.input, tt.defaultVal))
		})
	}
}
