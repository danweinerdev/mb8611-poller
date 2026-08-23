package docsis

import (
	"log/slog"
	"math"
	"strconv"
	"strings"
)

const upstreamColumnCount = 7

// ParseUpstream parses the delimited MotoConnUpstreamChannel value.
func ParseUpstream(raw string) []UpstreamChannel {
	if raw == "" {
		return nil
	}

	rows := strings.Split(raw, "|+|")
	channels := make([]UpstreamChannel, 0, len(rows))
	for rowNumber, row := range rows {
		if row == "" {
			continue
		}

		columns := strings.Split(row, "^")
		if len(columns) > 0 && columns[len(columns)-1] == "" {
			columns = columns[:len(columns)-1]
		}

		if len(columns) != upstreamColumnCount {
			slog.Warn("skipping malformed upstream channel row", "row", rowNumber, "columns", len(columns))
			continue
		}

		channel, channelOK := parseUpstreamInt(rowNumber, "Channel", columns[0])
		channelID, channelIDOK := parseUpstreamInt(rowNumber, "ChannelID", columns[3])
		symbolRate, symbolRateOK := parseUpstreamInt(rowNumber, "SymbRateKsps", columns[4])
		frequency, frequencyOK := parseUpstreamFloat(rowNumber, "FreqMHz", columns[5])
		power, powerOK := parseUpstreamFloat(rowNumber, "PowerDBmV", columns[6])
		if !(channelOK && channelIDOK && symbolRateOK && frequencyOK && powerOK) {
			slog.Warn("skipping malformed upstream channel row", "row", rowNumber)
			continue
		}

		channels = append(channels, UpstreamChannel{
			Channel:      channel,
			LockStatus:   columns[1],
			ChannelType:  columns[2],
			ChannelID:    channelID,
			SymbRateKsps: symbolRate,
			FreqMHz:      frequency,
			PowerDBmV:    power,
		})
	}

	return channels
}

func parseUpstreamInt(row int, field, raw string) (int, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 0)
	if err != nil {
		slog.Warn("failed to parse upstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0, false
	}
	return int(value), true
}

func parseUpstreamFloat(row int, field, raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		slog.Warn("failed to parse upstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0, false
	}
	return value, true
}
