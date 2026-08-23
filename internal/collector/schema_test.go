package collector

import (
	"reflect"
	"testing"

	monitor "github.com/danweinerdev/go-monitor"
)

func TestFixtureMetricSchemaContract(t *testing.T) {
	metrics := collectFixtureMetrics(t, fixtureClient())
	schemas := map[string]struct {
		tags   []string
		fields map[string]interface{}
	}{
		"docsis_downstream": {
			tags: []string{"host", "modem", "channel_id", "channel", "modulation", "lock_status"},
			fields: map[string]interface{}{
				"frequency_mhz": float64(0), "power_dbmv": float64(0), "snr_db": float64(0),
				"corrected": uint64(0), "uncorrected": uint64(0),
			},
		},
		"docsis_upstream": {
			tags: []string{"host", "modem", "channel_id", "channel", "channel_type", "lock_status"},
			fields: map[string]interface{}{
				"frequency_mhz": float64(0), "power_dbmv": float64(0), "symb_rate_ksps": int(0),
			},
		},
		"docsis_modem": {
			tags: []string{"host", "modem", "mac", "serial", "boot_status", "connectivity_status", "security_status", "firmware_version"},
			fields: map[string]interface{}{
				"uptime_seconds": float64(0), "ds_freq_mhz": float64(0),
			},
		},
		"docsis_event": {
			tags: []string{"host", "modem", "severity", "doc_id"},
			fields: map[string]interface{}{
				"message": string(""), "raw_date": string(""), "raw_time": string(""),
			},
		},
	}

	seen := make(map[string]bool, len(schemas))
	for _, metric := range metrics {
		schema, ok := schemas[metric.Measurement]
		if !ok {
			t.Errorf("unexpected measurement %q", metric.Measurement)
			continue
		}
		seen[metric.Measurement] = true
		assertMetricSchema(t, metric, schema.tags, schema.fields)
	}
	for measurement := range schemas {
		if !seen[measurement] {
			t.Errorf("fixture metrics missing measurement %q", measurement)
		}
	}
}

func assertMetricSchema(t *testing.T, metric *monitor.Metric, wantTags []string, wantFields map[string]interface{}) {
	t.Helper()
	if err := metric.Validate(); err != nil {
		t.Errorf("%s metric Validate() error = %v", metric.Measurement, err)
	}
	if len(metric.Tags) != len(wantTags) {
		t.Errorf("%s tag count = %d, want %d: %v", metric.Measurement, len(metric.Tags), len(wantTags), metric.Tags)
	}
	for _, tag := range wantTags {
		if _, ok := metric.Tags[tag]; !ok {
			t.Errorf("%s missing tag %q", metric.Measurement, tag)
		}
	}
	if len(metric.Fields) != len(wantFields) {
		t.Errorf("%s field count = %d, want %d: %v", metric.Measurement, len(metric.Fields), len(wantFields), metric.Fields)
	}
	for field, exemplar := range wantFields {
		value, ok := metric.Fields[field]
		if !ok {
			t.Errorf("%s missing field %q", metric.Measurement, field)
			continue
		}
		if got, want := reflect.TypeOf(value), reflect.TypeOf(exemplar); got != want {
			t.Errorf("%s field %q type = %v, want %v", metric.Measurement, field, got, want)
		}
		if _, ok := metric.Tags[field]; ok {
			t.Errorf("%s field %q is also a tag", metric.Measurement, field)
		}
	}
	for tag := range metric.Tags {
		if _, ok := metric.Fields[tag]; ok {
			t.Errorf("%s tag %q is also a field", metric.Measurement, tag)
		}
	}
}
