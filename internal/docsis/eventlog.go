package docsis

import (
	"crypto/sha1"
	"encoding/hex"
	"log/slog"
	"strings"
)

// ParseEventLog parses the delimited MotoStatusLogList value.
func ParseEventLog(raw string) []EventEntry {
	if raw == "" {
		return nil
	}

	rows := strings.Split(raw, "}-{")
	entries := make([]EventEntry, 0, len(rows))
	for rowNumber, row := range rows {
		columns := strings.SplitN(row, "^", 4)
		if len(columns) < 4 {
			slog.Warn("skipping malformed event log row", "row", rowNumber, "columns", len(columns))
			continue
		}

		entries = append(entries, EventEntry{
			Time:     columns[0],
			Date:     columns[1],
			Severity: columns[2],
			Message:  columns[3],
		})
	}

	return entries
}

// DocID returns the stable identifier for an event log entry.
func DocID(entry EventEntry) string {
	sum := sha1.Sum([]byte(entry.Date + "\x00" + entry.Time + "\x00" + entry.Message))
	return hex.EncodeToString(sum[:])
}
