package docsis

import (
	"log/slog"
	"math"
	"strconv"
	"strings"
)

const downstreamColumnCount = 9

// ParseDownstream parses the delimited MotoConnDownstreamChannel value.
func ParseDownstream(raw string) []DownstreamChannel {
	if raw == "" {
		return nil
	}

	rows := strings.Split(raw, "|+|")
	channels := make([]DownstreamChannel, 0, len(rows))
	for rowNumber, row := range rows {
		if row == "" {
			continue
		}

		columns := strings.Split(row, "^")
		if len(columns) > 0 && columns[len(columns)-1] == "" {
			columns = columns[:len(columns)-1]
		}

		if len(columns) != downstreamColumnCount {
			slog.Warn("skipping malformed downstream channel row", "row", rowNumber, "columns", len(columns))
			continue
		}

		channel, channelOK := parseDownstreamInt(rowNumber, "Channel", columns[0])
		channelID, channelIDOK := parseDownstreamInt(rowNumber, "ChannelID", columns[3])
		frequency, frequencyOK := parseDownstreamFloat(rowNumber, "FreqMHz", columns[4])
		power, powerOK := parseDownstreamFloat(rowNumber, "PowerDBmV", columns[5])
		snr, snrOK := parseDownstreamFloat(rowNumber, "SnrDB", columns[6])
		corrected, correctedOK := parseDownstreamUint(rowNumber, "Corrected", columns[7])
		uncorrected, uncorrectedOK := parseDownstreamUint(rowNumber, "Uncorrected", columns[8])
		if !(channelOK && channelIDOK && frequencyOK && powerOK && snrOK && correctedOK && uncorrectedOK) {
			slog.Warn("skipping malformed downstream channel row", "row", rowNumber)
			continue
		}

		channels = append(channels, DownstreamChannel{
			Channel:     channel,
			LockStatus:  columns[1],
			Modulation:  columns[2],
			ChannelID:   channelID,
			FreqMHz:     frequency,
			PowerDBmV:   power,
			SnrDB:       snr,
			Corrected:   corrected,
			Uncorrected: uncorrected,
		})
	}

	return channels
}

func parseDownstreamInt(row int, field, raw string) (int, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 0)
	if err != nil {
		slog.Warn("failed to parse downstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0, false
	}
	return int(value), true
}

func parseDownstreamFloat(row int, field, raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		slog.Warn("failed to parse downstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0, false
	}
	return value, true
}

func parseDownstreamUint(row int, field, raw string) (uint64, bool) {
	trimmed := strings.TrimSpace(raw)
	value, err := strconv.ParseUint(trimmed, 10, 64)
	if err == nil {
		return value, true
	}

	// Some MB8611 firmware renders overflowing 32-bit counters as signed
	// decimal values. Preserve their underlying counter bits.
	signed, signedErr := strconv.ParseInt(trimmed, 10, 32)
	if signedErr == nil && signed < 0 {
		return uint64(uint32(int32(signed))), true
	}

	slog.Warn("failed to parse downstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
	return 0, false
}
