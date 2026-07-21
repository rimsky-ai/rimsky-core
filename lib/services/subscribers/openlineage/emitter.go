// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// @concept: lineage
type Event struct {
	EventType   string       `json:"eventType"`
	EventTime   string       `json:"eventTime"`
	ProducerURI string       `json:"producer"`
	SchemaURL   string       `json:"schemaURL"`
	Run         RunRef       `json:"run"`
	Job         JobRef       `json:"job"`
	Inputs      []DatasetRef `json:"inputs,omitempty"`
	Outputs     []DatasetRef `json:"outputs,omitempty"`
}

type RunRef struct {
	RunID  string         `json:"runId"`
	Facets map[string]any `json:"facets,omitempty"`
}

type JobRef struct {
	Namespace string         `json:"namespace"`
	Name      string         `json:"name"`
	Facets    map[string]any `json:"facets,omitempty"`
}

type DatasetRef struct {
	Namespace string         `json:"namespace"`
	Name      string         `json:"name"`
	Facets    map[string]any `json:"facets,omitempty"`
}

const openLineageProducerURI = "https://github.com/rimsky-ai/rimsky-core/lib/services/subscribers/openlineage"

const openLineageRunEventSchemaURL = "https://openlineage.io/spec/1-0-5/OpenLineage.json#/$defs/RunEvent"

func rimskyFacet(name string, fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		out[k] = v
	}
	out["_producer"] = openLineageProducerURI
	out["_schemaURL"] = openLineageProducerURI + "/facets.json#/$defs/" + name
	return out
}

// @concept: lineage
type Emitter struct {
	BackendURL  string
	BearerToken string
	Client      *http.Client
}

type SendError struct {
	StatusCode int
	Err        error
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

func (e *SendError) Permanent() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

func NewEmitter(backendURL string, bearerToken string) *Emitter {
	return &Emitter{
		BackendURL:  backendURL,
		BearerToken: bearerToken,
		Client:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (e *Emitter) Send(ctx context.Context, ev Event) error {
	if e.BackendURL == "" {
		return fmt.Errorf("openlineage: backend URL not configured")
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
	if e.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.BearerToken)
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return &SendError{
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("openlineage backend %s → HTTP %d", url, resp.StatusCode),
		}
	}
	return nil
}

// @concept: lineage-record
func MakeLeafRunEvent(rec LeafRunRecord, observedAt time.Time, namespace string) Event {
	runID := rec.RunID
	jobName := rec.TemplateNodeAlias
	if jobName == "" {
		jobName = rec.NodeAlias
	}
	if jobName == "" {
		jobName = "unaliased-node-" + rec.NodeID
	}
	inputs := make([]DatasetRef, 0, len(rec.HeldClaims))
	for _, hc := range rec.HeldClaims {
		inputs = append(inputs, DatasetRef{
			Namespace: hc.ProducerName,
			Name:      hc.ScopeDataHash,
			Facets: map[string]any{
				"rimsky_held_claim": rimskyFacet("RimskyHeldClaim", map[string]any{
					"claim_handle_id": hc.ClaimHandleID,
					"role":            hc.Role,
				}),
			},
		})
	}
	runFacets := map[string]any{
		"rimsky": rimskyFacet("Rimsky", map[string]any{
			"node_id":              rec.NodeID,
			"frame_id":             rec.FrameID,
			"state":                rec.State,
			"scope_data_hash":      rec.ScopeDataHash,
			"error_class":          rec.ErrorClass,
			"template_hash":        rec.TemplateHash,
			"params_snapshot_hash": rec.ParamsSnapshotHash,
			"attributes_hash":      rec.AttributesHash,
			"executor_name":        rec.ExecutorName,
			"executor_version":     rec.ExecutorVersion,
			"frame_trigger_kind":   rec.FrameTriggerKind,
			"trigger_message_id":   rec.TriggerMessageID,
			"changed":              rec.Changed,
			"settling_signal_type": rec.SettlingSignalType,
			"terminal_kind":        rec.TerminalKind,
			"parent_run_id":        rec.ParentRunID,
			"substitution_refs":    rec.SubstitutionRefs,
		}),
	}
	return Event{
		EventType:   "COMPLETE",
		EventTime:   observedAt.UTC().Format(time.RFC3339Nano),
		ProducerURI: openLineageProducerURI,
		SchemaURL:   openLineageRunEventSchemaURL,
		Run:         RunRef{RunID: runID, Facets: runFacets},
		Job:         JobRef{Namespace: namespace, Name: jobName},
		Inputs:      inputs,
	}
}

// @concept: lineage-record
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
			"rimsky_claim_terminal": rimskyFacet("RimskyClaimTerminal", map[string]any{
				"version_id":      rec.VersionID,
				"claim_handle_id": rec.ClaimHandleID,
				"committed_at":    rec.CommittedAt,
				"outcome":         rec.Outcome,
				"cause":           rec.Cause,
			}),
		},
	}
	runID := rec.ClaimHandleID
	if rec.OpenLineageRunRef != "" {
		runID = rec.OpenLineageRunRef
	}
	runFacets := map[string]any{
		"rimsky": rimskyFacet("Rimsky", map[string]any{
			"sub_claim_handle_ids":      rec.SubClaimHandleIDs,
			"frame_id":                  rec.FrameID,
			"outcome":                   rec.Outcome,
			"cause":                     rec.Cause,
			"run_id":                    rec.RunID,
			"node_id":                   rec.NodeID,
			"parent_claim_handle_id":    rec.ParentClaimHandleID,
			"producer_metadata":         rec.ProducerMetadata,
			"terminating_supervisor_id": rec.TerminatingSupervisorID,
		}),
	}
	return Event{
		EventType:   eventType,
		EventTime:   observedAt.UTC().Format(time.RFC3339Nano),
		ProducerURI: openLineageProducerURI,
		SchemaURL:   openLineageRunEventSchemaURL,
		Run:         RunRef{RunID: runID, Facets: runFacets},
		Job:         JobRef{Namespace: namespace, Name: rec.ProducerName + jobSuffix},
		Outputs:     []DatasetRef{output},
	}
}
