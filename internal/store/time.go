package store

import (
	"fmt"
	"time"
)

// scanTime tolerates every timestamp representation found in the wild:
// values written by this binary (time.Time via modernc) and legacy rows
// written by mattn/go-sqlite3 (space-separated strings with offset).
func scanTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case []byte:
		return parseTimeString(string(t))
	case string:
		return parseTimeString(t)
	case nil:
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time type %T", v)
	}
}

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05",
}

func parseTimeString(s string) (time.Time, error) {
	for _, l := range timeLayouts {
		if ts, err := time.Parse(l, s); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable timestamp %q", s)
}
