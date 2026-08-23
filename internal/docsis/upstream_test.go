package docsis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseUpstreamFixture(t *testing.T) {
	channels := ParseUpstream(upstreamFixture(t))
	if len(channels) != 5 {
		t.Fatalf("channel count = %d, want 5", len(channels))
	}

	if got, want := channels[0], (UpstreamChannel{
		Channel:      1,
		LockStatus:   "Locked",
		ChannelType:  "SC-QAM",
		ChannelID:    1,
		SymbRateKsps: 5120,
		FreqMHz:      17.6,
		PowerDBmV:    42.3,
	}); got != want {
		t.Errorf("first channel = %+v, want %+v", got, want)
	}

	if got, want := channels[4], (UpstreamChannel{
		Channel:      5,
		LockStatus:   "Locked",
		ChannelType:  "OFDMA",
		ChannelID:    6,
		SymbRateKsps: 44000,
		FreqMHz:      36.8,
		PowerDBmV:    37.2,
	}); got != want {
		t.Errorf("last channel = %+v, want %+v", got, want)
	}
}

func TestParseUpstreamTrailingColumn(t *testing.T) {
	channels := ParseUpstream("1^Locked^SC-QAM^1^5120^17.6^40.3^")
	if got, want := channels, []UpstreamChannel{{
		Channel:      1,
		LockStatus:   "Locked",
		ChannelType:  "SC-QAM",
		ChannelID:    1,
		SymbRateKsps: 5120,
		FreqMHz:      17.6,
		PowerDBmV:    40.3,
	}}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("channels = %+v, want %+v", got, want)
	}
}

func TestParseUpstreamSkipsMalformedRows(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid numeric": "7^Locked^SC-QAM^8^not-a-number^17.6^40.3^",
		"missing column":  "7^Locked^SC-QAM^8^5120^17.6^",
		"extra column":    "7^Locked^SC-QAM^8^5120^17.6^40.3^extra",
		"non-finite":      "7^Locked^SC-QAM^8^5120^+Inf^40.3^",
	} {
		t.Run(name, func(t *testing.T) {
			if got := ParseUpstream(raw); len(got) != 0 {
				t.Fatalf("channels = %+v, want no channels", got)
			}
		})
	}
}

func TestParseUpstreamEmpty(t *testing.T) {
	if got := ParseUpstream(""); got != nil {
		t.Errorf("ParseUpstream(\"\") = %+v, want nil", got)
	}
}

func upstreamFixture(t *testing.T) string {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join("..", "hnap", "hnaptest", "testdata", "GetMotoStatusUpstreamChannelInfo.json"))
	if err != nil {
		t.Fatal(err)
	}

	var response struct {
		MotoConnUpstreamChannel string
	}
	if err := json.Unmarshal(contents, &response); err != nil {
		t.Fatal(err)
	}
	return response.MotoConnUpstreamChannel
}
