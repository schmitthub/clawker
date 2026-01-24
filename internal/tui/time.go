package tui

import (
	"fmt"
	"time"
)

// FormatRelative returns a human-readable relative time string like "2 hours ago".
func FormatRelative(t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	now := time.Now()
	diff := now.Sub(t)

	if diff < 0 {
		// Future time
		diff = -diff
		return formatFutureRelative(diff)
	}

	return formatPastRelative(diff)
}

func formatPastRelative(diff time.Duration) string {
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < 2*time.Minute:
		return "1 minute ago"
	case diff < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(diff.Minutes()))
	case diff < 2*time.Hour:
		return "1 hour ago"
	case diff < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(diff.Hours()))
	case diff < 48*time.Hour:
		return "yesterday"
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(diff.Hours()/24))
	case diff < 14*24*time.Hour:
		return "1 week ago"
	case diff < 30*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(diff.Hours()/(24*7)))
	case diff < 60*24*time.Hour:
		return "1 month ago"
	case diff < 365*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(diff.Hours()/(24*30)))
	case diff < 2*365*24*time.Hour:
		return "1 year ago"
	default:
		return fmt.Sprintf("%d years ago", int(diff.Hours()/(24*365)))
	}
}

func formatFutureRelative(diff time.Duration) string {
	switch {
	case diff < time.Minute:
		return "in a moment"
	case diff < 2*time.Minute:
		return "in 1 minute"
	case diff < time.Hour:
		return fmt.Sprintf("in %d minutes", int(diff.Minutes()))
	case diff < 2*time.Hour:
		return "in 1 hour"
	case diff < 24*time.Hour:
		return fmt.Sprintf("in %d hours", int(diff.Hours()))
	default:
		return fmt.Sprintf("in %d days", int(diff.Hours()/24))
	}
}

// FormatDuration returns a compact duration string like "2m 30s".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "-" + FormatDuration(-d)
	}
	if d == 0 {
		return "0s"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	switch {
	case hours > 0:
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		if seconds > 0 {
			return fmt.Sprintf("%dm %ds", minutes, seconds)
		}
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

// FormatTimestamp formats a time for display.
// If short is true, uses compact format like "15:04".
// If short is false, uses full format like "15:04:05".
func FormatTimestamp(t time.Time, short bool) string {
	if t.IsZero() {
		return "-"
	}

	now := time.Now()
	sameDay := t.Year() == now.Year() && t.YearDay() == now.YearDay()

	if sameDay {
		if short {
			return t.Format("15:04")
		}
		return t.Format("15:04:05")
	}

	// Different day
	if short {
		return t.Format("Jan 2 15:04")
	}
	return t.Format("Jan 2 15:04:05")
}

// FormatUptime formats a duration as an uptime string like "01:15:42".
func FormatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 99 {
		// For very long uptimes, show days
		days := hours / 24
		hours = hours % 24
		return fmt.Sprintf("%dd %02d:%02d:%02d", days, hours, minutes, seconds)
	}

	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

// FormatDate formats a date for display like "Jan 2, 2006".
func FormatDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("Jan 2, 2006")
}

// FormatDateTime formats a date and time like "Jan 2, 2006 15:04".
func FormatDateTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("Jan 2, 2006 15:04")
}

// ParseDurationOrDefault parses a duration string, returning defaultVal on error.
func ParseDurationOrDefault(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}
