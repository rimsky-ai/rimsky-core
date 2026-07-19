// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const commitResponseStampedVersion = "v-base-commit-7"

var commitResponseStampedMetadata = []byte("opaque\x00producer-bytes")

func TestCommitResponseFields_PlainNode_VersionIDPersisted(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities:           claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		CommitVersionID:        commitResponseStampedVersion,
		CommitProducerMetadata: commitResponseStampedMetadata,
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
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
				scenario.WithClaimProducers(scenario.AliasedClaimRef("cr-store", "items", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-commit-response-plain", map[string]any{})

	n := h.FindNode(iid, "plain-commit")
	require.NotNil(t, n, "plain-commit node missing")
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

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

func TestCommitResponseFields_FanOut_ProducerMetadataInParentWriteback(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities:           claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		CommitVersionID:        commitResponseStampedVersion,
		CommitProducerMetadata: commitResponseStampedMetadata,
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
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
				scenario.WithClaimProducers(scenario.AliasedClaimRef("cr-fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-story-commit-response-fanout", map[string]any{})

	parentNode := h.FindNode(iid, "cr-fan-parent")
	require.NotNil(t, parentNode, "cr-fan-parent node missing")
	h.WaitForNodeState(parentNode.ID, cascade.NodeStateFresh)

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
