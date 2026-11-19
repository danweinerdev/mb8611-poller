package docsis

import (
	"strings"
	"testing"
	"time"
)

func TestParseEventLogFixture(t *testing.T) {
	raw := eventLogFixture(t)
	entries := ParseEventLog(raw)
	if len(entries) == 0 {
		t.Fatal("event entry count = 0, want at least one")
	}

	if got, want := entries[0], (EventEntry{
		Date:     "Thu Aug 20 2026",
		Time:     "11:57:22",
		Severity: "Notice (6)",
		Message:  "US profile assignment change. US Chan ID: 8; Previous Profile: 11 13; New Profile: 9 13.;CM-MAC=XX:XX:XX:XX:XX:XX;CMTS-MAC=XX:XX:XX:XX:XX:XX;CM-QOS=1.1;CM-VER=3.1;",
	}); got != want {
		t.Errorf("first event entry = %+v, want %+v", got, want)
	}
	for i, entry := range entries {
		if entry.Date != strings.TrimSpace(entry.Date) || entry.Time != strings.TrimSpace(entry.Time) {
			t.Errorf("entry %d keeps surrounding whitespace: date=%q time=%q", i, entry.Date, entry.Time)
		}
		if _, ok := entry.Timestamp(time.UTC); !ok {
			t.Errorf("fixture entry %d has no parseable timestamp: date=%q time=%q", i, entry.Date, entry.Time)
		}
	}

	again := ParseEventLog(raw)
	if len(again) != len(entries) {
		t.Fatalf("second event entry count = %d, want %d", len(again), len(entries))
	}
	firstIDs, secondIDs := EventDocIDs(entries), EventDocIDs(again)
	seen := make(map[string]int, len(firstIDs))
	for i := range firstIDs {
		if firstIDs[i] != secondIDs[i] {
			t.Errorf("doc ID for entry %d = %q, want %q", i, secondIDs[i], firstIDs[i])
		}
		if previous, ok := seen[firstIDs[i]]; ok {
			t.Errorf("entries %d and %d share doc ID %q", previous, i, firstIDs[i])
		}
		seen[firstIDs[i]] = i
	}
}

func TestParseEventLogTrimsDateAndTime(t *testing.T) {
	entries := ParseEventLog(" 08:00:00 ^Fri Aug 21 2026\n^Notice (6)^first")
	want := EventEntry{Time: "08:00:00", Date: "Fri Aug 21 2026", Severity: "Notice (6)", Message: "first"}
	if len(entries) != 1 || entries[0] != want {
		t.Fatalf("event entries = %+v, want [%+v]", entries, want)
	}
}

func TestParseEventLogMalformedRow(t *testing.T) {
	entries := ParseEventLog("08:00:00^Fri Aug 21 2026^Notice^first}-{" +
		"malformed^row}-{" +
		"09:00:00^Fri Aug 21 2026^Critical^last")
	if got, want := entries, []EventEntry{
		{Time: "08:00:00", Date: "Fri Aug 21 2026", Severity: "Notice", Message: "first"},
		{Time: "09:00:00", Date: "Fri Aug 21 2026", Severity: "Critical", Message: "last"},
	}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("event entries = %+v, want %+v", got, want)
	}
}

func TestParseEventLogPreservesCaretsInMessage(t *testing.T) {
	entries := ParseEventLog("08:00:00^Fri Aug 21 2026^Notice^first^second^third")
	if got, want := entries, []EventEntry{{
		Time: "08:00:00", Date: "Fri Aug 21 2026", Severity: "Notice", Message: "first^second^third",
	}}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("event entries = %+v, want %+v", got, want)
	}
}

func TestParseEventLogEmpty(t *testing.T) {
	if got := ParseEventLog(""); got != nil {
		t.Errorf("ParseEventLog(\"\") = %+v, want nil", got)
	}
}

func TestEventTimestamp(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		entry EventEntry
		loc   *time.Location
		want  time.Time
		ok    bool
	}{
		{name: "utc", entry: EventEntry{Date: "Thu Aug 20 2026", Time: "11:57:22"}, loc: time.UTC, want: time.Date(2026, time.August, 20, 11, 57, 22, 0, time.UTC), ok: true},
		{name: "configured zone", entry: EventEntry{Date: "Thu Aug 20 2026", Time: "11:57:22"}, loc: chicago, want: time.Date(2026, time.August, 20, 16, 57, 22, 0, time.UTC), ok: true},
		{name: "raw whitespace", entry: EventEntry{Date: "Thu Aug 20 2026\n", Time: " 11:57:22"}, loc: time.UTC, want: time.Date(2026, time.August, 20, 11, 57, 22, 0, time.UTC), ok: true},
		{name: "padded single digit day", entry: EventEntry{Date: "Tue Sep  1 2026", Time: "01:02:03"}, loc: time.UTC, want: time.Date(2026, time.September, 1, 1, 2, 3, 0, time.UTC), ok: true},
		{name: "nil location is UTC", entry: EventEntry{Date: "Thu Aug 20 2026", Time: "11:57:22"}, want: time.Date(2026, time.August, 20, 11, 57, 22, 0, time.UTC), ok: true},
		{name: "time not established", entry: EventEntry{Date: "Time Not Established", Time: "Time Not Established"}, loc: time.UTC},
		{name: "garbage time", entry: EventEntry{Date: "Thu Aug 20 2026", Time: "25:61:00"}, loc: time.UTC},
		{name: "unset modem clock epoch", entry: EventEntry{Date: "Thu Jan 1 1970", Time: "00:00:12"}, loc: time.UTC},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := test.entry.Timestamp(test.loc)
			if ok != test.ok {
				t.Fatalf("Timestamp() ok = %t, want %t (time %v)", ok, test.ok, got)
			}
			if ok && !got.Equal(test.want) {
				t.Fatalf("Timestamp() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestEventDocIDIncludesSeverity(t *testing.T) {
	entry := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:00", Severity: "Notice", Message: "event"}
	changedSeverity := entry
	changedSeverity.Severity = "Critical"

	if got, other := EventDocIDs([]EventEntry{entry})[0], EventDocIDs([]EventEntry{changedSeverity})[0]; got == other {
		t.Errorf("doc ID did not change with severity: %q", got)
	}
}

func TestEventDocIDTimedEntriesStableAcrossLogRotation(t *testing.T) {
	a := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:00", Severity: "Notice (6)", Message: "a"}
	b := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:01", Severity: "Notice (6)", Message: "b"}
	c := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:02", Severity: "Notice (6)", Message: "c"}

	before := EventDocIDs([]EventEntry{a, b})
	after := EventDocIDs([]EventEntry{b, c})
	if before[1] != after[0] {
		t.Errorf("timed entry ID changed after rotation: %q -> %q", before[1], after[0])
	}
	if lone := EventDocIDs([]EventEntry{b})[0]; lone != before[1] {
		t.Errorf("timed entry ID depends on its neighbours: %q vs %q", lone, before[1])
	}
}

func TestEventDocIDDisambiguatesIdenticalTimedEntries(t *testing.T) {
	entry := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:00", Severity: "Notice (6)", Message: "same"}
	ids := EventDocIDs([]EventEntry{entry, entry})
	if ids[0] == ids[1] {
		t.Fatalf("identical timed entries share doc ID %q", ids[0])
	}
	if lone := EventDocIDs([]EventEntry{entry})[0]; lone != ids[0] {
		t.Errorf("first occurrence ID = %q, want the lone-entry ID %q", ids[0], lone)
	}
}

func TestEventDocIDUntimedEntriesDoNotMergeAcrossReboots(t *testing.T) {
	untimed := EventEntry{Date: "Time Not Established", Time: "Time Not Established", Severity: "Critical (3)", Message: "No Ranging Response received - T3 time-out"}
	bootOne := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:00", Severity: "Notice (6)", Message: "before first reboot"}
	bootTwo := EventEntry{Date: "Sat Aug 22 2026", Time: "10:00:00", Severity: "Notice (6)", Message: "before second reboot"}

	ids := EventDocIDs([]EventEntry{bootOne, untimed, untimed, bootTwo, untimed})
	unique := map[string]bool{ids[1]: true, ids[2]: true, ids[4]: true}
	if len(unique) != 3 {
		t.Fatalf("untimed entries with the same message share doc IDs: %v", ids)
	}
}

func TestEventDocIDUntimedEntriesStableAcrossAppendAndRotation(t *testing.T) {
	untimed := EventEntry{Date: "Time Not Established", Time: "Time Not Established", Severity: "Critical (3)", Message: "T3 time-out"}
	old := EventEntry{Date: "Thu Aug 20 2026", Time: "08:00:00", Severity: "Notice (6)", Message: "old"}
	anchor := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:00", Severity: "Notice (6)", Message: "anchor"}
	later := EventEntry{Date: "Fri Aug 21 2026", Time: "09:05:00", Severity: "Notice (6)", Message: "later"}
	newest := EventEntry{Date: "Fri Aug 21 2026", Time: "09:10:00", Severity: "Notice (6)", Message: "newest"}

	poll1 := EventDocIDs([]EventEntry{old, anchor, untimed, untimed})
	// A new event is appended.
	poll2 := EventDocIDs([]EventEntry{old, anchor, untimed, untimed, later})
	// The oldest event rotates out of the modem's log buffer.
	poll3 := EventDocIDs([]EventEntry{anchor, untimed, untimed, later, newest})
	if poll1[2] != poll2[2] || poll1[3] != poll2[3] {
		t.Errorf("untimed IDs changed after an append: %v -> %v", poll1[2:4], poll2[2:4])
	}
	if poll2[2] != poll3[1] || poll2[3] != poll3[2] {
		t.Errorf("untimed IDs changed after head rotation: %v -> %v", poll2[2:4], poll3[1:3])
	}

	// Once the preceding anchor has rotated out, the run anchors to the
	// following timed entry, and further head rotation keeps that stable.
	poll4 := EventDocIDs([]EventEntry{untimed, untimed, later, newest})
	poll5 := EventDocIDs([]EventEntry{untimed, later, newest})
	if poll4[1] != poll5[0] {
		t.Errorf("untimed ID anchored to following entry changed after head rotation: %q -> %q", poll4[1], poll5[0])
	}
	if poll4[0] == poll4[1] {
		t.Errorf("untimed run entries share doc ID %q", poll4[0])
	}
}

func TestEventDocIDUntimedWithoutAnyTimedEntry(t *testing.T) {
	untimed := EventEntry{Date: "Time Not Established", Time: "Time Not Established", Severity: "Critical (3)", Message: "T3 time-out"}
	ids := EventDocIDs([]EventEntry{untimed, untimed})
	if ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("untimed-only IDs = %v, want two distinct non-empty IDs", ids)
	}
	if again := EventDocIDs([]EventEntry{untimed, untimed}); again[0] != ids[0] || again[1] != ids[1] {
		t.Fatalf("untimed-only IDs are not deterministic: %v vs %v", ids, again)
	}
}
