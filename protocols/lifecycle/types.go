package lifecycle

import "encoding/json"

// OnTemplateRegisteredRequest fires when a template is first registered
// (its content-hashed spec is persisted but not yet deployed under any
// movable tag).
type OnTemplateRegisteredRequest struct {
	TemplateHash string
	Spec         json.RawMessage
}

// OnTemplateDeployedRequest fires when one or more tags are pointed at a
// template hash. Tags is the set of tags newly attached.
type OnTemplateDeployedRequest struct {
	TemplateHash string
	Tags         []string
}

// OnTemplateUndeployedRequest fires when the last tag is removed from a
// template hash (the template is no longer reachable by tag, but its
// hashed spec persists).
type OnTemplateUndeployedRequest struct {
	TemplateHash string
}

// OnTemplateDeregisteredRequest fires when a template hash is fully
// deleted (no tags, no instances).
type OnTemplateDeregisteredRequest struct {
	TemplateHash string
}

// OnInstanceCreatedRequest fires when a new instance is created from a
// template hash.
type OnInstanceCreatedRequest struct {
	InstanceID   string
	TemplateHash string
	InstanceKey  string
	Params       json.RawMessage
}

// OnInstanceTerminatedRequest fires when an instance reaches the
// terminated state (rimsky_instances.terminated_at is set).
type OnInstanceTerminatedRequest struct {
	InstanceID         string
	TemplateHash       string
	TerminatedAtUnixMs int64
}
