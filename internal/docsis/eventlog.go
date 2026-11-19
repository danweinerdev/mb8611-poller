package docsis

import (
	"crypto/sha1" // #nosec G505 -- document identity only, not a security boundary.
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// eventTimeLayout is the joined form of an event's date and time columns, for
// example "Thu Aug 20 2026 11:57:22".
const eventTimeLayout = "Mon Jan 2 2006 15:04:05"

// minimumEventYear rejects dates from an unset modem clock (for example
// 1970), which would otherwise index events decades in the past.
const minimumEventYear = 2000

// ParseEventLog parses the delimited MotoStatusLogList value. Date and time
// columns are trimmed; the modem appends a newline to every date.
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
			Time:     strings.TrimSpace(columns[0]),
			Date:     strings.TrimSpace(columns[1]),
			Severity: columns[2],
			Message:  columns[3],
		})
	}

	return entries
}

// Timestamp returns the modem-reported occurrence time of entry, interpreted
// in loc (UTC when nil). It returns false for entries whose time is not
// established ("Time Not Established"), unparseable, or from an unset clock.
func (entry EventEntry) Timestamp(loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.UTC
	}
	date := strings.Join(strings.Fields(entry.Date), " ")
	clock := strings.TrimSpace(entry.Time)
	if date == "" || clock == "" {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation(eventTimeLayout, date+" "+clock, loc)
	if err != nil || parsed.Year() < minimumEventYear {
		return time.Time{}, false
	}
	return parsed, true
}

func (entry EventEntry) established() bool {
	_, ok := entry.Timestamp(time.UTC)
	return ok
}

// EventDocIDs returns a stable document identifier for each entry, in order.
//
// An entry with an established time is identified by its date, time,
// severity, and message alone, so its ID does not depend on its position in
// the modem's rolling log. A repeat of an identical timed entry in the same
// log gets an occurrence suffix.
//
// An entry without an established time is anchored to the nearest preceding
// timed entry and its offset after it; when no timed entry precedes it (the
// anchor has rotated out of the log), it is anchored to the nearest following
// timed entry and its offset before it. Both forms are unaffected by new
// events being appended and by old events rotating out, except for the single
// switch from the preceding to the following anchor. Only a log with no timed
// entry at all falls back to the row position.
func EventDocIDs(entries []EventEntry) []string {
	ids := make([]string, len(entries))
	occurrences := make(map[string]int)
	for i, entry := range entries {
		if !entry.established() {
			continue
		}
		key := hashParts("v2", "timed", entry.Date, entry.Time, entry.Severity, entry.Message)
		occurrence := occurrences[key]
		occurrences[key] = occurrence + 1
		if occurrence == 0 {
			ids[i] = key
		} else {
			ids[i] = hashParts("v2", "timed-repeat", key, strconv.Itoa(occurrence))
		}
	}

	for i, entry := range entries {
		if ids[i] != "" {
			continue
		}
		ids[i] = untimedDocID(entries, ids, i, entry)
	}
	return ids
}

func untimedDocID(entries []EventEntry, ids []string, index int, entry EventEntry) string {
	for anchor := index - 1; anchor >= 0; anchor-- {
		if entries[anchor].established() {
			return hashParts("v2", "untimed-after", ids[anchor], strconv.Itoa(index-anchor), entry.Severity, entry.Message)
		}
	}
	for anchor := index + 1; anchor < len(entries); anchor++ {
		if entries[anchor].established() {
			return hashParts("v2", "untimed-before", ids[anchor], strconv.Itoa(anchor-index), entry.Severity, entry.Message)
		}
	}
	return hashParts("v2", "untimed-row", strconv.Itoa(index), entry.Severity, entry.Message)
}

func hashParts(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "\x00"))) // #nosec G401 -- document identity only.
	return hex.EncodeToString(sum[:])
}
