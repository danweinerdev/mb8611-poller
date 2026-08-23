package docsis

import (
	"log/slog"
	"strconv"
	"strings"
)

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

		channels = append(channels, DownstreamChannel{
			Channel:     parseDownstreamInt(rowNumber, "Channel", downstreamColumn(columns, 0)),
			LockStatus:  downstreamColumn(columns, 1),
			Modulation:  downstreamColumn(columns, 2),
			ChannelID:   parseDownstreamInt(rowNumber, "ChannelID", downstreamColumn(columns, 3)),
			FreqMHz:     parseDownstreamFloat(rowNumber, "FreqMHz", downstreamColumn(columns, 4)),
			PowerDBmV:   parseDownstreamFloat(rowNumber, "PowerDBmV", downstreamColumn(columns, 5)),
			SnrDB:       parseDownstreamFloat(rowNumber, "SnrDB", downstreamColumn(columns, 6)),
			Corrected:   parseDownstreamUint(rowNumber, "Corrected", downstreamColumn(columns, 7)),
			Uncorrected: parseDownstreamUint(rowNumber, "Uncorrected", downstreamColumn(columns, 8)),
		})
	}

	return channels
}

func downstreamColumn(columns []string, index int) string {
	if index >= len(columns) {
		return ""
	}
	return columns[index]
}

func parseDownstreamInt(row int, field, raw string) int {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 0)
	if err != nil {
		slog.Warn("failed to parse downstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0
	}
	return int(value)
}

func parseDownstreamFloat(row int, field, raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		slog.Warn("failed to parse downstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
		return 0
	}
	return value
}

func parseDownstreamUint(row int, field, raw string) uint64 {
	trimmed := strings.TrimSpace(raw)
	value, err := strconv.ParseUint(trimmed, 10, 64)
	if err == nil {
		return value
	}

	// Some MB8611 firmware renders overflowing 32-bit counters as signed
	// decimal values. Preserve their underlying counter bits.
	signed, signedErr := strconv.ParseInt(trimmed, 10, 32)
	if signedErr == nil && signed < 0 {
		return uint64(uint32(int32(signed)))
	}

	slog.Warn("failed to parse downstream channel numeric field", "row", row, "field", field, "value", raw, "error", err)
	return 0
}
