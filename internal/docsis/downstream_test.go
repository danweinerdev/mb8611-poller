package docsis

import "testing"

func TestParseDownstreamFixture(t *testing.T) {
	channels := ParseDownstream(downstreamFixture(t))
	if len(channels) != 34 {
		t.Fatalf("channel count = %d, want 34", len(channels))
	}

	if got, want := channels[0], (DownstreamChannel{
		Channel:     1,
		LockStatus:  "Locked",
		Modulation:  "QAM256",
		ChannelID:   45,
		FreqMHz:     237,
		PowerDBmV:   3,
		SnrDB:       43.6,
		Corrected:   411,
		Uncorrected: 1499,
	}); got != want {
		t.Errorf("first channel = %+v, want %+v", got, want)
	}

	if got, want := channels[32], (DownstreamChannel{
		Channel:     33,
		LockStatus:  "Locked",
		Modulation:  "OFDM PLC",
		ChannelID:   159,
		FreqMHz:     300,
		PowerDBmV:   2.7,
		SnrDB:       43.4,
		Corrected:   1653610435,
		Uncorrected: 12319,
	}); got != want {
		t.Errorf("OFDM channel = %+v, want %+v", got, want)
	}

	if got, want := channels[33], (DownstreamChannel{
		Channel:     34,
		LockStatus:  "Locked",
		Modulation:  "OFDM PLC",
		ChannelID:   160,
		FreqMHz:     950,
		PowerDBmV:   1.1,
		SnrDB:       41.7,
		Corrected:   3097357284,
		Uncorrected: 4599,
	}); got != want {
		t.Errorf("last channel = %+v, want %+v", got, want)
	}
}

func TestParseDownstreamReinterpretsSignedCounter(t *testing.T) {
	channels := ParseDownstream("1^Locked^QAM256^2^100^1^40^-1197610012^3^")
	if len(channels) != 1 {
		t.Fatalf("channel count = %d, want 1", len(channels))
	}
	if got, want := channels[0].Corrected, uint64(3097357284); got != want {
		t.Fatalf("corrected = %d, want %d", got, want)
	}
}

func TestParseDownstreamSkipsMalformedRows(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid numeric": "7^Locked^QAM256^8^not-a-number^2.5^41.2^12^34^",
		"missing column":  "7^Locked^QAM256^8^100^2.5^41.2^12^",
		"extra column":    "7^Locked^QAM256^8^100^2.5^41.2^12^34^extra",
		"non-finite":      "7^Locked^QAM256^8^NaN^2.5^41.2^12^34^",
	} {
		t.Run(name, func(t *testing.T) {
			if got := ParseDownstream(raw); len(got) != 0 {
				t.Fatalf("channels = %+v, want no channels", got)
			}
		})
	}
}

func TestParseDownstreamEmpty(t *testing.T) {
	if got := ParseDownstream(""); got != nil {
		t.Errorf("ParseDownstream(\"\") = %+v, want nil", got)
	}
}
