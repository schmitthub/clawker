package config

// This file owns the monitoring unit manifest schema (monitoring.yaml) — a
// self-contained observability contribution (OpenSearch indices, ingest
// pipelines, dashboards, collector routing) shipped as a monitoring component
// on the embedded floor, a loose convention dir, or an installed bundle. The
// loader that reads and validates a unit directory lives in internal/monitor
// (its sole consumer); config owns only the persisted shape, mirroring the
// harness/stack manifest split.
//
// Terminology: "monitoring unit" is the internal name; every user-facing
// surface (docs, monitor.extensions, schema descriptions) says "monitoring
// extension". The two are the same thing.

// MonitoringUnitManifest is the parsed monitoring.yaml.
type MonitoringUnitManifest struct {
	Description string `yaml:"description,omitempty" label:"Description" desc:"Human-readable summary of what this monitoring extension observes."`

	// Logs declares the OpenSearch log lanes this unit owns: for each
	// lane, the index it writes and the untrusted-lane service.name
	// values the collector routes into it. At least one lane is
	// required — a unit exists to land telemetry somewhere.
	Logs []MonitoringLogLane `yaml:"logs" label:"Logs" desc:"OpenSearch log lanes this extension owns; each lane names the index it writes and the untrusted-lane service.name values the collector routes into it. At least one lane is required."`

	// Metrics optionally declares unit metric handling on the shared
	// untrusted metrics pipeline. Omit for units whose metrics need no
	// collector-side shaping.
	Metrics *MonitoringUnitMetrics `yaml:"metrics,omitempty" label:"Metrics" desc:"Optional collector-side shaping for this extension's metrics on the shared untrusted metrics pipeline; omit when no reshaping is needed."`
}

// MonitoringLogLane declares one OpenSearch index the unit owns and the
// service.name values routed to it from the untrusted OTLP lane. The
// trusted (mTLS) lane is infra-only and deliberately unsayable from a
// unit manifest.
type MonitoringLogLane struct {
	Index        string   `yaml:"index"         label:"Index"         desc:"OpenSearch index this lane writes telemetry into."`
	ServiceNames []string `yaml:"service_names" label:"Service Names" desc:"Untrusted-lane service.name values the collector routes into this lane's index."`

	// Retention selects the lane's ISM participation: empty or
	// MonitoringRetentionDefault joins the shared clawker retention
	// policy; MonitoringRetentionCustom means the unit ships its own
	// policy files under ism-policies/, pattern-scoped to unit-owned
	// indices.
	Retention string `yaml:"retention,omitempty" label:"Retention" desc:"ISM participation for the lane: 'default' (or empty) joins the shared clawker retention policy; 'custom' means the extension ships its own ism-policies/ files scoped to its indices."`
}

// MonitoringUnitMetrics declares collector-side shaping for the unit's
// metrics on the shared untrusted pipeline.
type MonitoringUnitMetrics struct {
	// ServiceNames scopes the datapoint renames below; empty defaults
	// to the union of the unit's log-lane service names.
	ServiceNames []string `yaml:"service_names,omitempty" label:"Service Names" desc:"Metric service.name values the renames below apply to; empty defaults to the union of the extension's log-lane service names."`

	// DatapointRenames moves a datapoint attribute to a new key when
	// present, scoped to the unit's service names. Rendered as OTTL
	// transform statements by the collector-config generator.
	DatapointRenames []MetricRename `yaml:"datapoint_renames,omitempty" label:"Datapoint Renames" desc:"Datapoint attribute renames applied to this extension's metrics, scoped to its service names; each from/to pair moves the attribute (the source key is removed)."`
}

// MetricRename is one datapoint attribute rename: From's value lands on To and From is removed.
type MetricRename struct {
	From string `yaml:"from" label:"From" desc:"Source datapoint attribute key to rename; removed after its value moves to the target key."`
	To   string `yaml:"to"   label:"To"   desc:"Destination datapoint attribute key the source value lands on."`
}

// Monitoring lane retention vocabulary.
const (
	MonitoringRetentionDefault = "default"
	MonitoringRetentionCustom  = "custom"
)
