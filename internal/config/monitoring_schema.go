package config

// This file owns the monitoring unit manifest schema (monitoring.yaml) — a
// self-contained observability contribution (OpenSearch indices, ingest
// pipelines, dashboards, collector routing) shipped as a monitoring component
// on the embedded floor, a loose convention dir, or an installed bundle. The
// loader that reads and validates a unit directory lives in internal/monitor
// (its sole consumer); config owns only the persisted shape, mirroring the
// harness/stack manifest split.

// MonitoringUnitManifest is the parsed monitoring.yaml.
type MonitoringUnitManifest struct {
	Description string `yaml:"description,omitempty"`

	// Logs declares the OpenSearch log lanes this unit owns: for each
	// lane, the index it writes and the untrusted-lane service.name
	// values the collector routes into it. At least one lane is
	// required — a unit exists to land telemetry somewhere.
	Logs []MonitoringLogLane `yaml:"logs"`

	// Metrics optionally declares unit metric handling on the shared
	// untrusted metrics pipeline. Omit for units whose metrics need no
	// collector-side shaping.
	Metrics *MonitoringUnitMetrics `yaml:"metrics,omitempty"`
}

// MonitoringLogLane declares one OpenSearch index the unit owns and the
// service.name values routed to it from the untrusted OTLP lane. The
// trusted (mTLS) lane is infra-only and deliberately unsayable from a
// unit manifest.
type MonitoringLogLane struct {
	Index        string   `yaml:"index"`
	ServiceNames []string `yaml:"service_names"`

	// Retention selects the lane's ISM participation: empty or
	// MonitoringRetentionDefault joins the shared clawker retention
	// policy; MonitoringRetentionCustom means the unit ships its own
	// policy files under ism-policies/, pattern-scoped to unit-owned
	// indices.
	Retention string `yaml:"retention,omitempty"`
}

// MonitoringUnitMetrics declares collector-side shaping for the unit's
// metrics on the shared untrusted pipeline.
type MonitoringUnitMetrics struct {
	// ServiceNames scopes the datapoint renames below; empty defaults
	// to the union of the unit's log-lane service names.
	ServiceNames []string `yaml:"service_names,omitempty"`

	// DatapointRenames copies a datapoint attribute to a new key when
	// present, scoped to the unit's service names. Rendered as OTTL
	// transform statements by the collector-config generator.
	DatapointRenames []MetricRename `yaml:"datapoint_renames,omitempty"`
}

// MetricRename is one datapoint attribute copy: From's value lands on To.
type MetricRename struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// Monitoring lane retention vocabulary.
const (
	MonitoringRetentionDefault = "default"
	MonitoringRetentionCustom  = "custom"
)
