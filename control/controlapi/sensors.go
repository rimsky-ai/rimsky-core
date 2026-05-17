// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensors.go — F3. Sensor observation push endpoint.
//
//   - POST /sensors/{watch_id}/observations
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sensors / Observation flow.
//
// @concept: sensor
// @concept: message
//
// The sensor service pushes an observation body; control-api resolves
// `watch_id` to the row in `table:rimsky_sensor_watches`, applies the
// `on_observation.payload_template` substitution against the observation
// body, constructs a message envelope, and enqueues via
// `runtime.EnqueueMessage`. The watch row's `last_observed_at` advances
// in the same transaction.
//
// Observation bytes are treated as inert (@blessed-invariant 21):
// reads happen only through walkPath-style field lookups against
// well-known keys named by the template's payload_template. The
// handler never logs the body or formats it with `%v`.

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/runtime"
)

// registerSensorObservationsRoutes wires the sensor observation push.
func registerSensorObservationsRoutes(r chi.Router, deps AppDeps) {
	r.Post("/sensors/{watch_id}/observations", handleSensorObservation(deps))
}

// sensorObservationRequest is the body shape of
// POST /sensors/{watch_id}/observations.
type sensorObservationRequest struct {
	Observation map[string]any `json:"observation"`
}

// sensorObservationResponse echoes the enqueued message id so the
// sensor service can correlate.
type sensorObservationResponse struct {
	MessageID string `json:"message_id"`
}

// onObservationConfig mirrors `foundation/spec.OnObservationSpec` —
// duplicated here in the controlapi-local shape so the handler can
// json-decode the watch row's `on_observation` JSONB column without
// importing the graph layer for a row-time decode. The shape stays
// pinned to the spec; any field rename here breaks parity.
type onObservationConfig struct {
	TargetNode      string         `json:"target_node"`
	MessageKind     string         `json:"message_kind"`
	PayloadTemplate map[string]any `json:"payload_template,omitempty"`
}

// handleSensorObservation handles the per-watch observation push.
func handleSensorObservation(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		watchIDStr := chi.URLParam(req, "watch_id")
		watchID, err := uuid.Parse(watchIDStr)
		if err != nil {
			badRequest(w, "invalid watch_id")
			return
		}
		var body sensorObservationRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if body.Observation == nil {
			body.Observation = map[string]any{}
		}

		// Resolve the watch row outside the enqueue tx so the lookup
		// can return 404 without taking a write lock.
		watch, err := deps.Persist.SensorWatches().Get(req.Context(), shared.UUID(watchID))
		if err != nil {
			writeError(w, err)
			return
		}
		if watch == nil {
			notFoundResp(w, "watch not found")
			return
		}
		if watch.State != persistence.SensorWatchStateActive {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "watch is not active",
				"state": watch.State,
			})
			return
		}

		// Decode the on_observation JSONB. Bad data here is a control-
		// plane bug (the canonicalizer should have rejected malformed
		// configs), but we still emit a precise 500 instead of panicking.
		var cfg onObservationConfig
		if len(watch.OnObservation) > 0 {
			if err := json.Unmarshal(watch.OnObservation, &cfg); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error": "on_observation config corrupted: " + err.Error(),
				})
				return
			}
		}
		if cfg.MessageKind == "" {
			cfg.MessageKind = "invalidate"
		}

		// Apply payload_template substitution against the observation
		// body. Per spec §Observation flow, the template is JSON-shaped
		// with `{{observation.path.to.field}}` leaves; non-template
		// leaves pass through verbatim.
		resolvedPayload, err := substituteObservationTemplate(cfg.PayloadTemplate, body.Observation)
		if err != nil {
			badRequest(w, "payload_template substitution failed: "+err.Error())
			return
		}
		payloadBytes, err := json.Marshal(resolvedPayload)
		if err != nil {
			writeError(w, err)
			return
		}

		msgID := shared.UUID(uuid.New())
		now := deps.Clock.Now().UTC()
		enqueueReq := persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: watch.InstanceID,
			Kind:       cfg.MessageKind,
			Sender:     watch.SensorName,
			SenderKind: "sensor",
			Target:     cfg.TargetNode,
			Payload:    payloadBytes,
			ReceivedAt: now,
		}
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			// Re-verify the instance hasn't terminated since we read
			// the watch row — terminated instances reject new messages.
			inst, err := deps.Persist.Instances().Get(ctx, watch.InstanceID, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			if inst.TerminatedAt != nil {
				return errInstanceTerminated
			}
			if err := runtime.EnqueueMessage(ctx, tx, deps.Persist.Messages(), enqueueReq); err != nil {
				return err
			}
			lastObs := now
			return deps.Persist.SensorWatches().Update(ctx, tx, shared.UUID(watchID), persistence.SensorWatchUpdate{
				LastObservedAt: &lastObs,
			})
		})
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			if errors.Is(err, errInstanceTerminated) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, sensorObservationResponse{MessageID: msgID.String()})
	}
}

// substituteObservationTemplate walks `tmpl` and replaces leaf string
// values of the form `{{observation.<path>}}` with the corresponding
// value looked up in `obs`. Non-template strings and non-string leaves
// pass through. Nested maps and arrays recurse.
//
// The substitution is intentionally minimal: it mirrors the
// `graph/attribute/substitution.go::walkPath` discipline of treating
// observation bytes as inert (read by named-field path only), without
// pulling in the full template-rendering engine. The graph-side
// substitution layer is the canonical site for any expansion of the
// template language (filters, defaults, etc.).
func substituteObservationTemplate(tmpl map[string]any, obs map[string]any) (map[string]any, error) {
	if tmpl == nil {
		// Default behaviour: pass the observation through verbatim
		// when no payload_template is configured. The receiver still
		// reads via `{{trigger.message.payload.X}}` so the shape it
		// sees is the observation as-is.
		return obs, nil
	}
	out, err := substituteValue(tmpl, obs)
	if err != nil {
		return nil, err
	}
	m, ok := out.(map[string]any)
	if !ok {
		// substituteValue preserves shape; a map in maps out.
		return nil, errors.New("payload_template did not resolve to a JSON object")
	}
	return m, nil
}

func substituteValue(v any, obs map[string]any) (any, error) {
	switch val := v.(type) {
	case string:
		return resolveObservationLeaf(val, obs)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, inner := range val {
			r, err := substituteValue(inner, obs)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, inner := range val {
			r, err := substituteValue(inner, obs)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return val, nil
	}
}

// resolveObservationLeaf returns `s` unchanged unless it's exactly
// `{{observation.<path>}}` — in which case the dotted `<path>` is
// resolved against `obs`. Mixed-string templates ("prefix-{{...}}")
// are out of scope for V1 (the graph-side substitution layer is the
// home for the richer grammar); they pass through verbatim.
func resolveObservationLeaf(s string, obs map[string]any) (any, error) {
	if !strings.HasPrefix(s, "{{") || !strings.HasSuffix(s, "}}") {
		return s, nil
	}
	inner := strings.TrimSpace(s[2 : len(s)-2])
	const prefix = "observation"
	if inner != prefix && !strings.HasPrefix(inner, prefix+".") {
		// Not an observation reference (e.g. `{{params.x}}`); leave
		// the string verbatim. The substitution layer at dispatch
		// time resolves richer prefixes if needed.
		return s, nil
	}
	if inner == prefix {
		return obs, nil
	}
	path := strings.Split(inner[len(prefix)+1:], ".")
	return walkObsPath(obs, path), nil
}

// walkObsPath walks a dotted path through a JSON-shaped map. Returns
// nil when any segment is absent or the parent is not a map.
func walkObsPath(node any, path []string) any {
	for _, seg := range path {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node, ok = m[seg]
		if !ok {
			return nil
		}
	}
	return node
}

// touch keeps the time import live when the file compiles under a
// build tag that excludes the substitution helpers above.
var _ = time.Time{}
