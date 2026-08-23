package docsis

import "testing"

func TestParseEventLogFixture(t *testing.T) {
	raw := eventLogFixture(t)
	entries := ParseEventLog(raw)
	if len(entries) == 0 {
		t.Fatal("event entry count = 0, want at least one")
	}

	if got, want := entries[0], (EventEntry{
		Date:     "Thu Aug 20 2026\n",
		Time:     "11:57:22",
		Severity: "Notice (6)",
		Message:  "US profile assignment change. US Chan ID: 8; Previous Profile: 11 13; New Profile: 9 13.;CM-MAC=XX:XX:XX:XX:XX:XX;CMTS-MAC=XX:XX:XX:XX:XX:XX;CM-QOS=1.1;CM-VER=3.1;",
	}); got != want {
		t.Errorf("first event entry = %+v, want %+v", got, want)
	}

	again := ParseEventLog(raw)
	if len(again) != len(entries) {
		t.Fatalf("second event entry count = %d, want %d", len(again), len(entries))
	}
	for i, entry := range entries {
		if got, want := DocID(again[i]), DocID(entry); got != want {
			t.Errorf("DocID for entry %d = %q, want %q", i, got, want)
		}
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

func TestDocIDExcludesSeverity(t *testing.T) {
	entry := EventEntry{Date: "Fri Aug 21 2026", Time: "09:00:00", Severity: "Notice", Message: "event"}
	changedSeverity := entry
	changedSeverity.Severity = "Critical"

	if got, want := DocID(changedSeverity), DocID(entry); got != want {
		t.Errorf("DocID with changed severity = %q, want %q", got, want)
	}
}
