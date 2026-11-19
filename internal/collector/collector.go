// Package collector adapts HNAP modem responses into monitor metrics.
package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
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

// maxFirstSeenEvents bounds the first-seen cache for events without an
// established modem time. The cache only ever holds entries present in the
// latest event log, which on an MB8611 is far smaller than this.
var maxFirstSeenEvents = 4096

// errCooldown marks a poll skipped because of a lockout or auth-failure
// cooldown. Its text is the docsis_poll error field value.
var errCooldown = errors.New("lockout cooldown")

// Config controls metric tags and lockout handling for a Collector.
type Config struct {
	Host           string
	ModemTag       string
	LockoutBackoff time.Duration
	// EventLocation is the time zone of the modem's event-log times. Nil
	// means UTC.
	EventLocation *time.Location
}

// Collector performs one serial HNAP polling cycle at a time.
type Collector struct {
	client hnap.Client
	cfg    Config
	logger *slog.Logger

	pollMu sync.Mutex
	// firstSeen maps the doc_id of each event without an established modem
	// time to when this process first saw it. Guarded by pollMu.
	firstSeen map[string]time.Time

	mu           sync.Mutex
	lockoutUntil time.Time
	clock        func() time.Time
}

// NewCollector constructs a Collector using client to poll the modem.
func NewCollector(client hnap.Client, cfg Config, logger *slog.Logger) *Collector {
	if cfg.ModemTag == "" {
		cfg.ModemTag = cfg.Host
	}
	if cfg.EventLocation == nil {
		cfg.EventLocation = time.UTC
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Collector{
		client:    client,
		cfg:       cfg,
		logger:    logger,
		clock:     time.Now,
		firstSeen: make(map[string]time.Time),
	}
}

// MakeCollectFunc returns a function that performs one modem poll.
//
// Every poll, including a failed or skipped one, returns a docsis_poll status
// point. Poll failures are therefore reported in that point and logged here,
// and the returned error is always nil: go-monitor drops every metric of a
// poll whose CollectFunc returns an error.
func MakeCollectFunc(c *Collector) monitor.CollectFunc {
	return func(ctx context.Context) ([]*monitor.Metric, error) {
		c.pollMu.Lock()
		defer c.pollMu.Unlock()

		start := c.now()
		metrics, err := c.poll(ctx)
		duration := c.now().Sub(start)
		switch {
		case err == nil:
		case errors.Is(err, errCooldown):
			// Logged by poll with the remaining time.
		default:
			c.logger.Error("modem poll failed", "error", err, "duration", duration)
		}
		return append(metrics, c.pollStatusMetric(start, duration, err)), nil
	}
}

func (c *Collector) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock()
}

func (c *Collector) poll(ctx context.Context) ([]*monitor.Metric, error) {
	c.mu.Lock()
	now := c.clock()
	if now.Before(c.lockoutUntil) {
		remaining := c.lockoutUntil.Sub(now)
		c.mu.Unlock()
		c.logger.Warn("skipping poll during lockout cooldown", "remaining", remaining)
		return nil, errCooldown
	}
	c.mu.Unlock()

	if err := c.client.EnsureLogin(ctx); err != nil {
		c.maybeEnterCooldown(err)
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
		c.maybeEnterCooldown(err)
		return nil, err
	}

	baseTags := map[string]string{"host": c.cfg.Host, "modem": c.cfg.ModemTag}
	metrics := make([]*monitor.Metric, 0)
	collectedAt := c.now()

	if raw, ok := responseFor(c, responses, downstreamAction); ok {
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
	}

	if raw, ok := responseFor(c, responses, upstreamAction); ok {
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
	}

	connection, connectionOK := parseConnection(c, responses)
	software, softwareOK := parseSoftware(c, responses)
	startup, startupOK := parseStartup(c, responses)
	if connectionOK && softwareOK && startupOK {
		metrics = append(metrics, c.modemMetric(connection, software, startup, baseTags, collectedAt))
	}

	if raw, ok := responseFor(c, responses, logAction); ok {
		var response struct {
			Entries string `json:"MotoStatusLogList"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			c.logger.Warn("failed to parse HNAP response", "action", logAction, "error", err)
		} else {
			metrics = append(metrics, c.eventMetrics(docsis.ParseEventLog(response.Entries), baseTags, collectedAt)...)
		}
	}

	return metrics, nil
}

// maybeEnterCooldown starts the quiet period after a lockout or a rejected
// login. Retrying a rejected password every interval is what trips the
// modem's lockout, so both get the same cooldown.
func (c *Collector) maybeEnterCooldown(err error) {
	switch {
	case errors.Is(err, hnap.ErrLockedOut):
		c.enterLockoutCooldown("modem lockout detected, entering cooldown")
	case errors.Is(err, hnap.ErrAuthFailed):
		c.enterLockoutCooldown("modem rejected credentials, entering cooldown to avoid lockout")
	}
}

func (c *Collector) enterLockoutCooldown(message string) {
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
		c.logger.Warn(message, "until", until)
	}
}

func (c *Collector) pollStatusMetric(start time.Time, duration time.Duration, err error) *monitor.Metric {
	success, message := 1, ""
	if err != nil {
		success, message = 0, err.Error()
	}
	return &monitor.Metric{
		Measurement: "docsis_poll",
		Timestamp:   start,
		Tags:        map[string]string{"host": c.cfg.Host, "modem": c.cfg.ModemTag},
		Fields: map[string]interface{}{
			"success":     success,
			"duration_ms": float64(duration) / float64(time.Millisecond),
			"error":       message,
		},
	}
}

func responseFor(c *Collector, responses map[string]json.RawMessage, action string) (json.RawMessage, bool) {
	raw, ok := responses[action+"Response"]
	if !ok {
		c.logger.Warn("HNAP response missing action", "action", action)
		return nil, false
	}

	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		c.logger.Warn("failed to parse HNAP action response", "action", action, "error", err)
		return nil, false
	}
	resultRaw, ok := response[action+"Result"]
	if !ok {
		c.logger.Warn("HNAP action response missing result", "action", action)
		return nil, false
	}
	var result string
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		c.logger.Warn("failed to parse HNAP action result", "action", action, "error", err)
		return nil, false
	}
	if result != "OK" {
		c.logger.Warn("HNAP action response was not OK", "action", action, "result", result)
		return nil, false
	}
	return raw, true
}

func parseConnection(c *Collector, responses map[string]json.RawMessage) (docsis.ConnectionInfo, bool) {
	raw, ok := responseFor(c, responses, connectionAction)
	if !ok {
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
	raw, ok := responseFor(c, responses, softwareAction)
	if !ok {
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
	raw, ok := responseFor(c, responses, startupAction)
	if !ok {
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

// Channel series are keyed by channel_id only. Status strings are fields, so
// a modulation or lock change does not start a new series.
func downstreamMetric(channel docsis.DownstreamChannel, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["channel_id"] = strconv.Itoa(channel.ChannelID)
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
			"locked":        boolInt(strings.EqualFold(strings.TrimSpace(channel.LockStatus), "Locked")),
			"lock_status":   channel.LockStatus,
			"modulation":    channel.Modulation,
		},
	}
}

func upstreamMetric(channel docsis.UpstreamChannel, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["channel_id"] = strconv.Itoa(channel.ChannelID)
	return &monitor.Metric{
		Measurement: "docsis_upstream",
		Timestamp:   timestamp,
		Tags:        tags,
		Fields: map[string]interface{}{
			"frequency_mhz":  channel.FreqMHz,
			"power_dbmv":     channel.PowerDBmV,
			"symb_rate_ksps": channel.SymbRateKsps,
			"locked":         boolInt(strings.EqualFold(strings.TrimSpace(channel.LockStatus), "Locked")),
			"lock_status":    channel.LockStatus,
			"channel_type":   channel.ChannelType,
		},
	}
}

// modemMetric emits the modem status point. A numeric value that fails to
// parse is left out (with a warning) rather than dropping the whole point or
// substituting a value of another type.
func (c *Collector) modemMetric(connection docsis.ConnectionInfo, software docsis.Software, startup docsis.StartupSequence, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["mac"] = software.MacAddress
	tags["serial"] = software.SerialNum
	tags["firmware_version"] = software.SfVer

	fields := map[string]interface{}{
		"boot_status":         startup.BootStatus,
		"connectivity_status": startup.ConnectivityStatus,
		"security_status":     startup.SecurityStatus,
		"boot_ok":             boolInt(strings.EqualFold(strings.TrimSpace(startup.BootStatus), "OK")),
		"connectivity_ok":     boolInt(strings.EqualFold(strings.TrimSpace(startup.ConnectivityStatus), "OK")),
		"security_enabled":    boolInt(strings.EqualFold(strings.TrimSpace(startup.SecurityStatus), "Enabled")),
	}
	if uptime, ok := connection.Uptime(); ok {
		fields["uptime_seconds"] = uptime.Seconds()
	} else {
		c.logger.Warn("omitting unparseable modem uptime", "uptime", connection.SystemUpTime)
	}
	if frequency, ok := frequencyMHz(startup.DSFreq); ok {
		fields["ds_freq_mhz"] = frequency
	} else {
		c.logger.Warn("omitting unparseable modem downstream frequency", "ds_frequency", startup.DSFreq)
	}

	return &monitor.Metric{
		Measurement: "docsis_modem",
		Timestamp:   timestamp,
		Tags:        tags,
		Fields:      fields,
	}
}

// eventMetrics maps the event log to docsis_event points. An event with an
// established modem time uses that time; one without keeps the time this
// process first saw it, so its timestamp does not move from poll to poll.
// The first-seen cache is rebuilt from each successfully parsed log, so it
// never holds more than the entries currently on the modem, and is capped at
// maxFirstSeenEvents.
func (c *Collector) eventMetrics(entries []docsis.EventEntry, baseTags map[string]string, collectedAt time.Time) []*monitor.Metric {
	ids := docsis.EventDocIDs(entries)
	firstSeen := make(map[string]time.Time)
	overflow := false
	metrics := make([]*monitor.Metric, 0, len(entries))
	for i, entry := range entries {
		timestamp, ok := entry.Timestamp(c.cfg.EventLocation)
		if !ok {
			seen, known := c.firstSeen[ids[i]]
			if !known {
				seen = collectedAt
			}
			timestamp = seen
			if _, kept := firstSeen[ids[i]]; kept || len(firstSeen) < maxFirstSeenEvents {
				firstSeen[ids[i]] = seen
			} else {
				overflow = true
			}
		}
		metrics = append(metrics, eventMetric(entry, ids[i], baseTags, timestamp))
	}
	if overflow {
		c.logger.Warn("event first-seen cache is full; some untimed events use the poll time", "limit", maxFirstSeenEvents)
	}
	c.firstSeen = firstSeen
	return metrics
}

func eventMetric(entry docsis.EventEntry, docID string, baseTags map[string]string, timestamp time.Time) *monitor.Metric {
	tags := copyTags(baseTags)
	tags["severity"] = entry.Severity
	tags["doc_id"] = docID
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

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func copyTags(tags map[string]string) map[string]string {
	copy := make(map[string]string, len(tags)+4)
	for key, value := range tags {
		copy[key] = value
	}
	return copy
}

func frequencyMHz(raw string) (float64, bool) {
	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[1] != "Hz" {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}
	return value / 1_000_000, true
}
