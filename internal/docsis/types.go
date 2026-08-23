// Package docsis parses DOCSIS status values returned by the modem.
package docsis

// DownstreamChannel is one row from MotoConnDownstreamChannel.
type DownstreamChannel struct {
	Channel     int
	LockStatus  string
	Modulation  string
	ChannelID   int
	FreqMHz     float64
	PowerDBmV   float64
	SnrDB       float64
	Corrected   uint64
	Uncorrected uint64
}
