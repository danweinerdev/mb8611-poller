// Package collector adapts HNAP modem responses into monitor metrics.
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/mb8611-poller/internal/docsis"
	"github.com/danweinerdev/mb8611-poller/internal/hnap"
)

const (
	downstreamAction = "GetMotoStatusDownstreamChannelInfo"
	upstreamAction   = "GetMotoStatusUpstreamChannelInfo"
	connectionAction = "GetMotoStatusConnectionInfo"
	softwareAction   = "GetMotoStatusSoftware"
	startupAction    = "GetMotoStatusStartupSequence"
	logAction        = "GetMotoStatusLog"
)

// Config controls metric tags and lockout handling for a Collector.
type Config struct {
	Host           string
	ModemTag       string
	LockoutBackoff time.Duration
}

// Collector performs one serial HNAP polling cycle at a time.
type Collector struct {
	client hnap.Client
	cfg    Config
	logger *slog.Logger

	mu           sync.Mutex
	lockoutUntil time.Time
	clock        func() time.Time
}

// NewCollector constructs a Collector using client to poll the modem.
func NewCollector(client hnap.Client, cfg Config, logger *slog.Logger) *Collector {
	if cfg.ModemTag == "" {
		cfg.ModemTag = cfg.Host
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Collector{
		client: client,
		cfg:    cfg,
		logger: logger,
		clock:  time.Now,
	}
}

// MakeCollectFunc returns a function that performs one modem poll.
func MakeCollectFunc(c *Collector) monitor.CollectFunc {
	return func(ctx context.Context) ([]*monitor.Metric, error) {
		c.mu.Lock()
		now := c.clock()
		if now.Before(c.lockoutUntil) {
			remaining := c.lockoutUntil.Sub(now)
			c.mu.Unlock()
			c.logger.Warn("skipping poll during lockout cooldown", "remaining", remaining)
			return nil, nil
		}
		c.mu.Unlock()

		if err := c.client.EnsureLogin(ctx); err != nil {
			if errors.Is(err, hnap.ErrLockedOut) {
				c.enterLockoutCooldown()
				return nil, nil
			}
			return nil, err
		}

		c.mu.Lock()
		c.lockoutUntil = time.Time{}
		c.mu.Unlock()

		responses, err := c.client.Multi(ctx,
			downstreamAction,
			upstreamAction,
			connectionAction,
			softwareAction,
			startupAction,
			logAction,
		)
		if err != nil {
			if errors.Is(err, hnap.ErrLockedOut) {
				c.enterLockoutCooldown()
				return nil, nil
			}
			return nil, err
		}

		baseTags := map[string]string{"host": c.cfg.Host, "modem": c.cfg.ModemTag}
		metrics := make([]*monitor.Metric, 0)
		collectedAt := c.clock()

		if raw, ok := responseFor(responses, downstreamAction); ok {
			var response struct {
				Channels string `json:"MotoConnDownstreamChannel"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				c.logger.Warn("failed to parse HNAP response", "action", downstreamAction, "error", err)
			} else {
				for _, channel := range docsis.ParseDownstream(response.Channels) {
					metrics = append(metrics, downstreamMetric(channel, baseTags, collectedAt))
				}
			}
		} else {
			c.logger.Warn("HNAP response missing action", "action", downstreamAction)
		}

		if raw, ok := responseFor(responses, upstreamAction); ok {
			var response struct {
				Channels string `json:"MotoConnUpstreamChannel"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				c.logger.Warn("failed to parse HNAP response", "action", upstreamAction, "error", err)
			} else {
				for _, channel := range docsis.ParseUpstream(response.Channels) {
					metrics = append(metrics, upstreamMetric(channel, baseTags, collectedAt))
				}
			}
		} else {
			c.logger.Warn("HNAP response missing action", "action", upstreamAction)
		}

		connection, connectionOK := parseConnection(c, responses)
		software, softwareOK := parseSoftware(c, responses)
		startup, startupOK := parseStartup(c, responses)
		if connectionOK && softwareOK && startupOK {
			metrics = append(metrics, modemMetric(connection, software, startup, baseTags, collectedAt))
		}

		if raw, ok := responseFor(responses, logAction); ok {
			var response struct {
				Entries string `json:"MotoStatusLogList"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				c.logger.Warn("failed to parse HNAP response", "action", logAction, "error", err)
			} else {
				for _, entry := range docsis.ParseEventLog(response.Entries) {
					metrics = append(metrics, eventMetric(entry, baseTags, collectedAt))
				}
			}
		} else {
			c.logger.Warn("HNAP response missing action", "action", logAction)
		}

		return metrics, nil
	}
}

func (c *Collector) enterLockoutCooldown() {
	enteredCooldown := false
	c.mu.Lock()
	now := c.clock()
	until := c.lockoutUntil
	if !now.Before(c.lockoutUntil) {
		until = now.Add(c.cfg.LockoutBackoff)
		c.lockoutUntil = until
		enteredCooldown = true
	}
	c.mu.Unlock()
	if enteredCooldown {
		c.logger.Warn("modem lockout detected, entering cooldown", "until", until)
	}
}

func responseFor(responses map[string]json.RawMessage, action string) (json.RawMessage, bool) {
	response, ok := responses[action+"Response"]
	return response, ok
}

func parseConnection(c *Collector, responses map[string]json.RawMessage) (docsis.ConnectionInfo, bool) {
	raw, ok := responseFor(responses, connectionAction)
	if !ok {
		c.logger.Warn("HNAP response missing action", "action", connectionAction)
		return docsis.ConnectionInfo{}, false
	}
	connection, err := docsis.ParseConnectionInfo(raw)
	if err != nil {
		c.logger.Warn("failed to parse HNAP response", "action", connectionAction, "error", err)
		return docsis.ConnectionInfo{}, false
	}
	if connection.NetworkAccess == "" && connection.SystemUpTime == "" {
		c.logger.Warn("HNAP response contained no connection data", "action", connectionAction)
		return docsis.ConnectionInfo{}, false
	}
	return connection, true
}

func parseSoftware(c *Collector, responses map[string]json.RawMessage) (docsis.Software, bool) {
	raw, ok := responseFor(responses, softwareAction)
	if !ok {
		c.logger.Warn("HNAP response missing action", "action", softwareAction)
		return docsis.Software{}, false
	}
	software, err := docsis.ParseSoftware(raw)
	if err != nil {
		c.logger.Warn("failed to parse HNAP response", "action", softwareAction, "error", err)
		return docsis.Software{}, false
	}
	if software.SfVer == "" && software.MacAddress == "" && software.SerialNum == "" {
		c.logger.Warn("HNAP response contained no software data", "action", softwareAction)
		return docsis.Software{}, false
	}
	return software, true
}

func parseStartup(c *Collector, responses map[string]json.RawMessage) (docsis.StartupSequence, bool) {
	raw, ok := responseFor(responses, startupAction)
	if !ok {
		c.logger.Warn("HNAP response missing action", "action", startupAction)
		return docsis.StartupSequence{}, false
	}
	startup, err := docsis.ParseStartupSequence(raw)
	if err != nil {
		c.logger.Warn("failed to parse HNAP response", "action", startupAction, "error", err)
		return docsis.StartupSequence{}, false
	}
	if startup.BootStatus == "" && startup.ConnectivityStatus == "" && startup.SecurityStatus == "" {
		c.logger.Warn("HNAP response contained no startup data", "action", startupAction)
		return docsis.StartupSequence{}, false
	}
	return startup, true
}

func downstreamMetric(channel docsis.DownstreamChannel, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["channel_id"] = strconv.Itoa(channel.ChannelID)
	tags["channel"] = strconv.Itoa(channel.Channel)
	tags["modulation"] = channel.Modulation
	tags["lock_status"] = channel.LockStatus
	return &monitor.Metric{
		Measurement: "docsis_downstream",
		Timestamp:   timestamp,
		Tags:        tags,
		Fields: map[string]interface{}{
			"frequency_mhz": channel.FreqMHz,
			"power_dbmv":    channel.PowerDBmV,
			"snr_db":        channel.SnrDB,
			"corrected":     channel.Corrected,
			"uncorrected":   channel.Uncorrected,
		},
	}
}

func upstreamMetric(channel docsis.UpstreamChannel, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["channel_id"] = strconv.Itoa(channel.ChannelID)
	tags["channel"] = strconv.Itoa(channel.Channel)
	tags["channel_type"] = channel.ChannelType
	tags["lock_status"] = channel.LockStatus
	return &monitor.Metric{
		Measurement: "docsis_upstream",
		Timestamp:   timestamp,
		Tags:        tags,
		Fields: map[string]interface{}{
			"frequency_mhz":  channel.FreqMHz,
			"power_dbmv":     channel.PowerDBmV,
			"symb_rate_ksps": channel.SymbRateKsps,
		},
	}
}

func modemMetric(connection docsis.ConnectionInfo, software docsis.Software, startup docsis.StartupSequence, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["mac"] = software.MacAddress
	tags["serial"] = software.SerialNum
	tags["boot_status"] = startup.BootStatus
	tags["connectivity_status"] = startup.ConnectivityStatus
	tags["security_status"] = startup.SecurityStatus
	tags["firmware_version"] = software.SfVer

	uptime, _ := connection.Uptime()
	return &monitor.Metric{
		Measurement: "docsis_modem",
		Timestamp:   timestamp,
		Tags:        tags,
		Fields: map[string]interface{}{
			"uptime_seconds": uptime.Seconds(),
			"ds_freq_mhz":    frequencyMHz(startup.DSFreq),
		},
	}
}

func eventMetric(entry docsis.EventEntry, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["severity"] = entry.Severity
	tags["doc_id"] = docsis.DocID(entry)
	return &monitor.Metric{
		Measurement: "docsis_event",
		Timestamp:   timestamp,
		Tags:        tags,
		Fields: map[string]interface{}{
			"message":  entry.Message,
			"raw_date": entry.Date,
			"raw_time": entry.Time,
		},
	}
}

func copyTags(tags map[string]string) map[string]string {
	copy := make(map[string]string, len(tags)+4)
	for key, value := range tags {
		copy[key] = value
	}
	return copy
}

func frequencyMHz(raw string) float64 {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return value / 1_000_000
}
