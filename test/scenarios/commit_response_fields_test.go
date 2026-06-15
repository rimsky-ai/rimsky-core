// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-commit-response-honored acceptance proof.
//
// Per the spec story
// .ok-planner/specs/2026-06-11-last-mile-stability-design.md
// (STORY-commit-response-honored): a claim-producer author sets
// `version_id` and `producer_metadata` on the base-protocol Commit
// response and sees them land where the protocol says — the
// claim-handle row's version and the fan-out parent's writeback — so
// the fields the proto documents are real for the base protocol, not
// only for the data-processing mix-in.
//
// Falsifier brief: "Base-protocol Commit response fields set by the
// producer and absent from the row / writeback — the response body
// still discarded." Pinned by TWO scenarios against the real assembled
// stack (scenario.Start boots supervisor + scheduler + control-api
// against real Postgres via testcontainers; the stub claim-producer is
// a real remote gRPC peer stamping both fields on its CommitResponse):
//
//   - (a) Plain node to terminal: the producer's base-Commit
//     `version_id` is persisted on the corresponding
//     rimsky_claim_handles row (queried on the post-terminal
//     state='committed' row, which persists per the claim-handle
//     retention window — i.e. before any sweep).
//   - (b) Fan-out: the children's base-Commit `producer_metadata` is
//     surfaced in the parent run's writeback row under the
//     `producer_metadata` key, one entry per partition key
//     (base64-encoded — the writeback row is JSON and cannot carry
//     raw bytes). The parent's own base-Commit `version_id` lands on
//     the parent claim-handle row as well.
//
// No stubbed integration points on the rimsky side: the Commit verb
// fires from the unified terminal-decision engine over the real gRPC
// producer client (lib/runtime/peer), and the writeback surfacing runs
// through the unified SettleChildren settlement path.
package scenarios

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// commitResponseStampedVersion / commitResponseStampedMetadata are the
// producer-side stamps the stub puts on every base CommitResponse.
// Metadata is deliberately non-JSON-shaped bytes to pin the
// inert-bytes contract (@blessed-invariant 20): rimsky must surface
// them verbatim (base64) without parsing.
const commitResponseStampedVersion = "v-base-commit-7"

var commitResponseStampedMetadata = []byte("opaque\x00producer-bytes")

// TestCommitResponseFields_PlainNode_VersionIDPersisted pins
// acceptance half (a): a plain (non-fan-out, non-data-processing) node
// reaching its success terminal fires the base-protocol Commit; the
// producer's `version_id` response field lands on the corresponding
// rimsky_claim_handles row.
func TestCommitResponseFields_PlainNode_VersionIDPersisted(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities:           claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		CommitVersionID:        commitResponseStampedVersion,
		CommitProducerMetadata: commitResponseStampedMetadata,
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"cr-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	h.Stub.WhenType("plain-commit").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-commit-response-plain", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "plain-commit", Executor: "stub"},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("cr-store", "items", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-commit-response-plain", map[string]any{})

	n := h.FindNode(iid, "plain-commit")
	require.NotNil(t, n, "plain-commit node missing")
	require.True(t,
		h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 60*time.Second),
		"plain-commit node must reach its success terminal")

	// @deliberate: The claim-handle row persists past terminal as state='committed'
	// (queried here well inside the retention window, before any
	// sweep). The base-Commit response's version_id must be on it —
	// the falsifier is exactly "set by the producer and absent from
	// the row".
	var versionID, state string
	require.Eventually(t, func() bool {
		var count int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_claim_handles
			 WHERE holder_node_id = $1 AND state = 'committed'
		`, []any{n.ID}, &count)
		return count == 1
	}, 60*time.Second, 50*time.Millisecond,
		"the plain node's claim-handle row must resolve to state='committed'")
	h.QueryRowSQL(`
		SELECT COALESCE(version_id, ''), state
		  FROM rimsky_claim_handles
		 WHERE holder_node_id = $1
	`, []any{n.ID}, &versionID, &state)
	require.Equal(t, "committed", state)
	require.Equal(t, commitResponseStampedVersion, versionID,
		"the producer's base-Commit version_id must be persisted on the claim-handle row "+
			"(today this worked only via the data-processing mix-in's commit-candidate path; "+
			"the base-protocol response body must not be discarded)")
}

// TestCommitResponseFields_FanOut_ProducerMetadataInParentWriteback
// pins acceptance half (b): a fan-out whose children's base-Commit
// responses carry `producer_metadata` sees it surfaced in the parent
// run's writeback row — one entry per partition key under the
// `producer_metadata` key, each the child's metadata bytes
// base64-encoded. Also asserts the parent's own base-Commit
// `version_id` landed on the parent claim-handle row (the same
// engine-side wiring on the aggregate-terminal path).
func TestCommitResponseFields_FanOut_ProducerMetadataInParentWriteback(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities:           claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		CommitVersionID:        commitResponseStampedVersion,
		CommitProducerMetadata: commitResponseStampedMetadata,
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"cr-fanout-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	h.Stub.WhenType("cr-fan-parent").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "story-commit-response-fanout", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "cr-fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindStrict},
					},
				},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("cr-fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-commit-response-fanout", map[string]any{})

	parentNode := h.FindNode(iid, "cr-fan-parent")
	require.NotNil(t, parentNode, "cr-fan-parent node missing")
	require.True(t,
		h.WaitForNodeState(parentNode.ID, cascade.NodeStateFresh, 90*time.Second),
		"parent fan-out node must reach its aggregate success terminal")

	// @deliberate: The parent claim handle — the row SplitScope partitioned,
	// distinguished from the leaves' own freshly-Open'd claims (which
	// share holder_node_id) by its expected_children_count > 0 —
	// resolves to committed once all three children settle; its
	// node_run_id is the parent fan-out run whose writeback row carries
	// the surfaced children metadata.
	require.Eventually(t, func() bool {
		var committedParents int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_claim_handles
			 WHERE holder_node_id = $1
			   AND parent_claim_handle_id IS NULL
			   AND expected_children_count > 0
			   AND state = 'committed'
		`, []any{parentNode.ID}, &committedParents)
		return committedParents == 1
	}, 90*time.Second, 50*time.Millisecond,
		"the parent claim-handle row must resolve to state='committed' after the children settle")

	// @deliberate: Every sub-claim child resolved committed (the children's commits
	// whose response metadata the writeback must surface).
	var committedChildren int
	h.QueryRowSQL(`
		SELECT COUNT(*)
		  FROM rimsky_claim_handles
		 WHERE parent_claim_handle_id IS NOT NULL
		   AND holder_node_id = $1
		   AND state = 'committed'
	`, []any{parentNode.ID}, &committedChildren)
	require.Equal(t, 3, committedChildren,
		"all three sub-claim children must resolve via Commit")

	// @deliberate: Parent's own base-Commit version_id (engine wiring on the
	// aggregate-terminal path).
	var parentVersionID string
	h.QueryRowSQL(`
		SELECT COALESCE(version_id, '')
		  FROM rimsky_claim_handles
		 WHERE holder_node_id = $1
		   AND parent_claim_handle_id IS NULL
		   AND expected_children_count > 0
	`, []any{parentNode.ID}, &parentVersionID)
	require.Equal(t, commitResponseStampedVersion, parentVersionID,
		"the parent's own base-Commit version_id must be persisted on the parent claim-handle row")

	// @deliberate: The fan-out parent run's writeback row must surface every
	// committed child's producer_metadata under the partition key.
	var writebackJSON string
	h.QueryRowSQL(`
		SELECT COALESCE(a.data::text, '{}')
		  FROM rimsky_node_attributes a
		 WHERE a.node_run_id = (
		   SELECT node_run_id
		     FROM rimsky_claim_handles
		    WHERE holder_node_id = $1
		      AND parent_claim_handle_id IS NULL
		      AND expected_children_count > 0
		 )
	`, []any{parentNode.ID}, &writebackJSON)

	var writeback map[string]any
	require.NoError(t, json.Unmarshal([]byte(writebackJSON), &writeback),
		"parent writeback row must be JSON")
	metaAny, ok := writeback["producer_metadata"]
	require.True(t, ok,
		"parent writeback must carry the producer_metadata key — the children's base-Commit "+
			"producer_metadata must not be discarded (writeback: %s)", writebackJSON)
	meta, ok := metaAny.(map[string]any)
	require.True(t, ok, "producer_metadata must be an object keyed by partition key")

	wantB64 := base64.StdEncoding.EncodeToString(commitResponseStampedMetadata)
	for _, pk := range []string{"a", "b", "c"} {
		require.Equal(t, wantB64, meta[pk],
			"partition %q: the child's base-Commit producer_metadata bytes must be surfaced "+
				"verbatim (base64) in the parent's writeback", pk)
	}
}
