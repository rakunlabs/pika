package sqlite

import (
	"strings"
	"time"
)

const timeFormat = time.RFC3339Nano

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func parseTime(s string) time.Time {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		// Fallback for other formats
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

// isUniqueViolation checks if a SQLite error is a UNIQUE constraint violation.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
