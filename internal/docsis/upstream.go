package docsis

import (
	"log/slog"
	"strconv"
	"strings"
)

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

		channels = append(channels, UpstreamChannel{
			Channel:      parseUpstreamInt(rowNumber, "Channel", upstreamColumn(columns, 0)),
			LockStatus:   upstreamColumn(columns, 1),
			ChannelType:  upstreamColumn(columns, 2),
			ChannelID:    parseUpstreamInt(rowNumber, "ChannelID", upstreamColumn(columns, 3)),
			SymbRateKsps: parseUpstreamInt(rowNumber, "SymbRateKsps", upstreamColumn(columns, 4)),
			FreqMHz:      parseUpstreamFloat(rowNumber, "FreqMHz", upstreamColumn(columns, 5)),
			PowerDBmV:    parseUpstreamFloat(rowNumber, "PowerDBmV", upstreamColumn(columns, 6)),
		})
	}

	return channels
}

func upstreamColumn(columns []string, index int) string {
	if index >= len(columns) {
		return ""
	}
	return columns[index]
}

func parseUpstreamInt(row int, field, raw string) int {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 0)
	if err != nil {
		slog.Warn("failed to parse upstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0
	}
	return int(value)
}

func parseUpstreamFloat(row int, field, raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		slog.Warn("failed to parse upstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0
	}
	return value
}
