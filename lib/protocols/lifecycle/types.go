// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import "encoding/json"

type OnTemplateRegisteredRequest struct {
	TemplateHash string
	Spec         json.RawMessage
}

type OnTemplateDeployedRequest struct {
	TemplateHash string
	Tags         []string
}

type OnTemplateUndeployedRequest struct {
	TemplateHash string
}

type OnTemplateDeregisteredRequest struct {
	TemplateHash string
}

type OnInstanceCreatedRequest struct {
	InstanceID   string
	TemplateHash string
	InstanceKey  string
	Params       json.RawMessage

	ServiceBindings json.RawMessage

	OwnerAPIKeyID string

	TargetRoutingIdentity string
}

type OnInstanceTerminatedRequest struct {
	InstanceID         string
	TemplateHash       string
	TerminatedAtUnixMs int64
}

type OnRunScopeTerminalRequest struct {
	RunScopeID     string
	TerminalReason string
	InstanceID     string
}
