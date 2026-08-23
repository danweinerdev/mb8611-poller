package docsis

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// ConnectionInfo contains the flat fields returned by GetMotoStatusConnectionInfo.
type ConnectionInfo struct {
	NetworkAccess string `json:"MotoConnNetworkAccess"`
	SystemUpTime  string `json:"MotoConnSystemUpTime"`
}

// Software contains the flat fields returned by GetMotoStatusSoftware.
type Software struct {
	SpecVer     string `json:"StatusSoftwareSpecVer"`
	HdVer       string `json:"StatusSoftwareHdVer"`
	SfVer       string `json:"StatusSoftwareSfVer"`
	MacAddress  string `json:"StatusSoftwareMac"`
	SerialNum   string `json:"StatusSoftwareSerialNum"`
	Certificate string `json:"StatusSoftwareCertificate"`
	CustomerVer string `json:"StatusSoftwareCustomerVer"`
}

// StartupSequence contains the flat fields returned by GetMotoStatusStartupSequence.
type StartupSequence struct {
	BootStatus               string `json:"MotoConnBootStatus"`
	BootComment              string `json:"MotoConnBootComment"`
	ConfigurationFileStatus  string `json:"MotoConnConfigurationFileStatus"`
	ConfigurationFileComment string `json:"MotoConnConfigurationFileComment"`
	ConnectivityStatus       string `json:"MotoConnConnectivityStatus"`
	ConnectivityComment      string `json:"MotoConnConnectivityComment"`
	DSFreq                   string `json:"MotoConnDSFreq"`
	DSComment                string `json:"MotoConnDSComment"`
	SecurityStatus           string `json:"MotoConnSecurityStatus"`
	SecurityComment          string `json:"MotoConnSecurityComment"`
}

type connectionInfoResponse struct {
	NetworkAccess string `json:"MotoConnNetworkAccess"`
	SystemUpTime  string `json:"MotoConnSystemUpTime"`
}

// ParseConnectionInfo parses a GetMotoStatusConnectionInfo response body.
func ParseConnectionInfo(raw json.RawMessage) (ConnectionInfo, error) {
	var response connectionInfoResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ConnectionInfo{}, fmt.Errorf("parse connection info: %w", err)
	}

	return ConnectionInfo(response), nil
}

type softwareResponse struct {
	SpecVer     string `json:"StatusSoftwareSpecVer"`
	HdVer       string `json:"StatusSoftwareHdVer"`
	SfVer       string `json:"StatusSoftwareSfVer"`
	MacAddress  string `json:"StatusSoftwareMac"`
	SerialNum   string `json:"StatusSoftwareSerialNum"`
	Certificate string `json:"StatusSoftwareCertificate"`
	CustomerVer string `json:"StatusSoftwareCustomerVer"`
}

// ParseSoftware parses a GetMotoStatusSoftware response body.
func ParseSoftware(raw json.RawMessage) (Software, error) {
	var response softwareResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return Software{}, fmt.Errorf("parse software: %w", err)
	}

	return Software(response), nil
}

type startupSequenceResponse struct {
	BootStatus               string `json:"MotoConnBootStatus"`
	BootComment              string `json:"MotoConnBootComment"`
	ConfigurationFileStatus  string `json:"MotoConnConfigurationFileStatus"`
	ConfigurationFileComment string `json:"MotoConnConfigurationFileComment"`
	ConnectivityStatus       string `json:"MotoConnConnectivityStatus"`
	ConnectivityComment      string `json:"MotoConnConnectivityComment"`
	DSFreq                   string `json:"MotoConnDSFreq"`
	DSComment                string `json:"MotoConnDSComment"`
	SecurityStatus           string `json:"MotoConnSecurityStatus"`
	SecurityComment          string `json:"MotoConnSecurityComment"`
}

// ParseStartupSequence parses a GetMotoStatusStartupSequence response body.
func ParseStartupSequence(raw json.RawMessage) (StartupSequence, error) {
	var response startupSequenceResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return StartupSequence{}, fmt.Errorf("parse startup sequence: %w", err)
	}

	return StartupSequence(response), nil
}

var uptimePattern = regexp.MustCompile(`^(\d+) days? (\d+)h:(\d+)m:(\d+)s$`)

// Uptime returns SystemUpTime as a duration when it uses the modem's known format.
// It returns false when the raw value cannot be parsed or would overflow a duration.
func (info ConnectionInfo) Uptime() (time.Duration, bool) {
	matches := uptimePattern.FindStringSubmatch(info.SystemUpTime)
	if matches == nil {
		return 0, false
	}

	values := [4]uint64{}
	for i := range values {
		value, err := strconv.ParseUint(matches[i+1], 10, 64)
		if err != nil {
			return 0, false
		}
		values[i] = value
	}

	const (
		secondsPerMinute  = uint64(60)
		secondsPerHour    = 60 * secondsPerMinute
		secondsPerDay     = 24 * secondsPerHour
		maxDurationSecond = uint64((1<<63 - 1) / int64(time.Second))
	)
	if values[2] >= secondsPerMinute || values[3] >= secondsPerMinute || values[0] > maxDurationSecond/secondsPerDay {
		return 0, false
	}

	seconds := values[0] * secondsPerDay
	components := [3]struct {
		value      uint64
		multiplier uint64
	}{
		{values[1], secondsPerHour},
		{values[2], secondsPerMinute},
		{values[3], 1},
	}
	for _, component := range components {
		if component.value > (maxDurationSecond-seconds)/component.multiplier {
			return 0, false
		}
		seconds += component.value * component.multiplier
	}

	return time.Duration(seconds) * time.Second, true
}
