// Package externalsql implements resource.Factory and resource.Resource for
// the external-SQL access kind. Committed results are written to a
// consumer-declared SQL table via a staging-table + atomic-swap pattern.
//
// Connections are provided externally as a named pool map (see Factory
// Connections field); the implementation does not open pools itself. Quality
// rules run over an in-memory row list prior to staging.
package externalsql

// configSchema describes the template-level config block accepted by this
// implementation. Template validation uses this schema to reject malformed
// access.config blocks early.
var configSchema = []byte(`{
  "type": "object",
  "required": ["connection_ref", "schema", "table", "primary_key"],
  "properties": {
    "connection_ref":  { "type": "string" },
    "schema":          { "type": "string" },
    "table":           { "type": "string" },
    "staging_table":   { "type": "string" },
    "previous_table":  { "type": "string" },
    "primary_key":     { "type": "array", "items": { "type": "string" }, "minItems": 1 },
    "keep_versions":   { "type": "integer", "minimum": 1 }
  },
  "additionalProperties": true
}`)

// instanceConfig is the typed post-parse view of the template config block
// plus instance-specific fields. Schema/Table must be set; Staging/Previous
// default to Table+"__staging" / Table+"__previous".
type instanceConfig struct {
	Schema        string
	Table         string
	StagingTable  string
	PreviousTable string
	PrimaryKey    []string
	KeepVersions  int
}

// loadConfig parses a resource.Config map into a typed instanceConfig. Missing
// staging/previous names are filled in with defaults.
func loadConfig(cfg map[string]any) instanceConfig {
	out := instanceConfig{
		Schema:        asString(cfg["schema"]),
		Table:         asString(cfg["table"]),
		StagingTable:  asString(cfg["staging_table"]),
		PreviousTable: asString(cfg["previous_table"]),
		PrimaryKey:    asStringSlice(cfg["primary_key"]),
		KeepVersions:  2,
	}
	if kv, ok := cfg["keep_versions"].(int); ok && kv > 0 {
		out.KeepVersions = kv
	} else if kvf, ok := cfg["keep_versions"].(float64); ok && kvf > 0 {
		out.KeepVersions = int(kvf)
	}
	if out.StagingTable == "" && out.Table != "" {
		out.StagingTable = out.Table + "__staging"
	}
	if out.PreviousTable == "" && out.Table != "" {
		out.PreviousTable = out.Table + "__previous"
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
