package iostreams

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
		return formatFutureRelative(-diff)
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

// FormatTimestamp formats a time as "2006-01-02 15:04:05".
func FormatTimestamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

// FormatUptime formats a duration as a human-readable uptime string like "2d 5h 30m".
// Negative durations are clamped to zero.
func FormatUptime(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	switch {
	case days > 0:
		if minutes > 0 {
			return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
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

// FormatDate formats a date for display like "Jan 15, 2024".
func FormatDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("Jan 2, 2006")
}

// FormatDateTime formats a date and time like "Jan 15, 2024 2:30 PM".
func FormatDateTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("Jan 2, 2006 3:04 PM")
}
