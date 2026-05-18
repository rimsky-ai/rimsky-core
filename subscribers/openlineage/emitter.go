// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Event is a minimal OpenLineage 1.x event envelope. Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §OpenLineage emitter. We hand-roll the JSON shape rather than
// depending on `github.com/OpenLineage/openlineage-go` so the
// subscriber stays self-contained (per the plan's pre-resolved design
// decision).
//
//	@concept: lineage
type Event struct {
	EventType   string         `json:"eventType"` // START | COMPLETE | FAIL | ABORT | OTHER
	EventTime   string         `json:"eventTime"` // RFC3339
	ProducerURI string         `json:"producer"`  // canonical rimsky URI
	SchemaURL   string         `json:"schemaURL"` // OpenLineage spec URL
	Run         RunRef         `json:"run"`
	Job         JobRef         `json:"job"`
	Inputs      []DatasetRef   `json:"inputs,omitempty"`
	Outputs     []DatasetRef   `json:"outputs,omitempty"`
	Facets      map[string]any `json:"facets,omitempty"`
}

// RunRef is the OpenLineage run identifier. `runId` is namespaced per
// rimsky as `instance_id + child_key` so fan-out children get distinct
// run ids while still sharing the same job alias.
type RunRef struct {
	RunID  string         `json:"runId"`
	Facets map[string]any `json:"facets,omitempty"`
}

// JobRef is the OpenLineage job identifier. `name` is the template's
// node alias (e.g. `"draft"`); `namespace` is operator-configured.
type JobRef struct {
	Namespace string         `json:"namespace"`
	Name      string         `json:"name"`
	Facets    map[string]any `json:"facets,omitempty"`
}

// DatasetRef is the OpenLineage dataset identifier. We map a held claim
// to `(namespace = producer_name, name = scope_data_hash)` per the
// spec §OpenLineage emitter / Dataset mapping.
type DatasetRef struct {
	Namespace string         `json:"namespace"`
	Name      string         `json:"name"`
	Facets    map[string]any `json:"facets,omitempty"`
}

// Emitter posts OpenLineage events to a configured backend. Per spec
// §OpenLineage emitter / Transport, V1 is HTTP POST to
// `{backend_url}/api/v1/lineage`.
//
//	@concept: lineage
type Emitter struct {
	BackendURL string
	Client     *http.Client
}

// NewEmitter returns an Emitter with a reasonable HTTP timeout.
func NewEmitter(backendURL string) *Emitter {
	return &Emitter{
		BackendURL: backendURL,
		Client:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Send POSTs an Event to the configured backend. Non-2xx response
// status is returned as a formatted error so the caller can log + retry
// on next poll. Empty BackendURL is a no-op (used by tests).
func (e *Emitter) Send(ctx context.Context, ev Event) error {
	if e.BackendURL == "" {
		return nil
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("openlineage marshal: %w", err)
	}
	url := e.BackendURL + "/api/v1/lineage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("openlineage backend %s → HTTP %d", url, resp.StatusCode)
	}
	return nil
}

// MakeLeafRunEvent maps a rimsky `record_kind = 'leaf_run'` lineage
// record to an OpenLineage `COMPLETE` event. The mapping follows spec
// §OpenLineage emitter:
//   - `instance_id + child_key` → `run.runId`.
//   - `template_node_alias` → `job.name`.
//   - `held_claims` → `inputs[]` keyed by `(producer_name, scope_data_hash)`.
//   - Frame-trigger metadata → custom facets (`triggerKind`, etc.).
//
// The `eventTime` is the lineage row's `observed_at`.
//
//	@concept: lineage-record
func MakeLeafRunEvent(rec LeafRunRecord, observedAt time.Time, instanceID string, namespace string) Event {
	runID := instanceID
	if rec.ChildKey != "" {
		runID = instanceID + "/" + rec.ChildKey
	}
	if runID == "" {
		runID = rec.RunID
	}
	jobName := rec.TemplateNodeAlias
	if jobName == "" {
		jobName = rec.NodeAlias
	}
	inputs := make([]DatasetRef, 0, len(rec.HeldClaims))
	for _, hc := range rec.HeldClaims {
		inputs = append(inputs, DatasetRef{
			Namespace: hc.ProducerName,
			Name:      hc.ScopeDataHash,
			Facets: map[string]any{
				"rimsky_claim_handle_id": hc.ClaimHandleID,
				"rimsky_claim_role":      hc.Role,
			},
		})
	}
	// Project the writer's full LeafRunRecord shape into the rimsky
	// facet block so downstream OL consumers can audit-trace the leaf
	// run back to its node-run row (`node_id`, `frame_id`, `state`,
	// `scope_data_hash`, `error_class`).
	facets := map[string]any{
		"rimsky": map[string]any{
			"node_id":              rec.NodeID,
			"frame_id":             rec.FrameID,
			"state":                rec.State,
			"scope_data_hash":      rec.ScopeDataHash,
			"error_class":          rec.ErrorClass,
			"template_hash":        rec.TemplateHash,
			"params_snapshot_hash": rec.ParamsSnapshotHash,
			"userdata_hash":        rec.UserdataHash,
			"executor_name":        rec.ExecutorName,
			"executor_version":     rec.ExecutorVersion,
			"frame_trigger_kind":   rec.FrameTriggerKind,
			"trigger_message_id":   rec.TriggerMessageID,
			"changed":              rec.Changed,
			"last_outcome":         rec.LastOutcome,
			"terminal_kind":        rec.TerminalKind,
			"parent_run_id":        rec.ParentRunID,
			"substitution_refs":    rec.SubstitutionRefs,
		},
	}
	return Event{
		EventType:   "COMPLETE",
		EventTime:   observedAt.UTC().Format(time.RFC3339Nano),
		ProducerURI: "https://github.com/fallguy/rimsky/subscribers/openlineage",
		SchemaURL:   "https://openlineage.io/spec/1-0-5/OpenLineage.json#/$defs/RunEvent",
		Run:         RunRef{RunID: runID},
		Job:         JobRef{Namespace: namespace, Name: jobName},
		Inputs:      inputs,
		Facets:      facets,
	}
}

// MakeClaimTerminalEvent maps a rimsky `record_kind = 'claim_terminal'`
// lineage record to an OpenLineage `COMPLETE` (for committed claims) or
// `ABORT` (for abandoned / force-cancelled claims) event. Per spec
// §OpenLineage emitter / Dataset mapping.
//
//	@concept: lineage-record
func MakeClaimTerminalEvent(rec ClaimTerminalRecord, observedAt time.Time, namespace string) Event {
	eventType := "COMPLETE"
	jobSuffix := ".commit"
	switch rec.Outcome {
	case "abandoned", "force_cancelled":
		eventType = "ABORT"
		jobSuffix = ".abandon"
	}
	output := DatasetRef{
		Namespace: rec.ProducerName,
		Name:      rec.ScopeDataHash,
		Facets: map[string]any{
			"rimsky_version_id":      rec.VersionID,
			"rimsky_claim_handle_id": rec.ClaimHandleID,
			"rimsky_committed_at":    rec.CommittedAt,
			"rimsky_outcome":         rec.Outcome,
			"rimsky_cause":           rec.Cause,
		},
	}
	runID := rec.ClaimHandleID
	if rec.OpenLineageRunRef != "" {
		runID = rec.OpenLineageRunRef
	}
	return Event{
		EventType:   eventType,
		EventTime:   observedAt.UTC().Format(time.RFC3339Nano),
		ProducerURI: "https://github.com/fallguy/rimsky/subscribers/openlineage",
		SchemaURL:   "https://openlineage.io/spec/1-0-5/OpenLineage.json#/$defs/RunEvent",
		Run:         RunRef{RunID: runID},
		Job:         JobRef{Namespace: namespace, Name: rec.ProducerName + jobSuffix},
		Outputs:     []DatasetRef{output},
		Facets: map[string]any{
			"rimsky": map[string]any{
				"sub_claim_handle_ids":   rec.SubClaimHandleIDs,
				"frame_id":               rec.FrameID,
				"outcome":                rec.Outcome,
				"cause":                  rec.Cause,
				"run_id":                 rec.RunID,
				"node_id":                rec.NodeID,
				"parent_claim_handle_id": rec.ParentClaimHandleID,
				"producer_metadata":      rec.ProducerMetadata,
			},
		},
	}
}
