// Held-claim runtime: spec §5.6.3 / §5.6.4 wiring.
//
// At commit of a node holding a `claim: true, hold: true` lock, the
// supervisor inserts one `rimsky_claim_holders` row per terminal-leaf
// of the §11.4 holding subgraph, recording the picked claim id and the
// per-leaf on_commit / on_give_up disposition.
//
// At commit of a node declaring `claim_resolutions`, the supervisor
// looks up the matching holder rows (keyed by the resolving node id)
// and runs the §5.6.4 reference-counted resolution algorithm via
// `claimstorepg.Store.ResolveOnTerminal`.
//
// All three operations run inside the outer release transaction so the
// claim ledger commits or rolls back atomically with the lock-holder
// release. Failures roll back the whole tx.
package supervisor

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

// insertHeldClaimHolders walks acq.Locks for every Hold:true claim
// acquisition and inserts one rimsky_claim_holders row per terminal-leaf
// of the §11.4 holding subgraph. The leaf node-type set is resolved
// from the source's NodeDef in the template spec (acq.NodeDef is the
// CURRENT node — the source — not a downstream); the per-instance node
// IDs are looked up via storage.Nodes().ListByInstance.
//
// Runs inside the supplied tx so the holder rows commit or roll back
// together with lock release. A non-Hold claim is a no-op for this
// helper; non-claim locks are ignored entirely.
//
// Per-row on_commit / on_give_up come from the source's NodeStoreRef
// (template-level override) when set, falling back to the resolution
// node's per-store-ref override (NodeDef.Stores), then to the store's
// own defaults (handled by claimstorepg.Store; no caller-side fallback
// needed).
func insertHeldClaimHolders(
	ctx context.Context, args RunArgs, tx pgx.Tx, acq *acquisition,
) error {
	if acq.NodeDef == nil {
		return nil
	}
	tmpl, instanceNodes, err := loadTemplateAndInstanceNodes(ctx, args, acq.InstanceID)
	if err != nil {
		return fmt.Errorf("insertHeldClaimHolders: %w", err)
	}
	if tmpl == nil {
		return nil
	}
	stx := pgstorage.WrapPgxTx(tx)
	for _, lk := range acq.Locks {
		spec, ok := lk.Spec.(store.ClaimLockSpec)
		if !ok || !spec.Hold {
			continue
		}
		storeName := lk.Handle.StoreName
		if storeName == "" {
			storeName = spec.StoreName
		}
		// §11.4 walk over the template (node-type-keyed).
		leafTypes := node.FindHoldingTerminals(&tmpl.Spec, acq.NodeType, storeName)
		if len(leafTypes) == 0 {
			continue
		}
		// Filter to leaves that actually carry a matching claim_resolutions
		// entry: validation should have rejected the template on deploy if
		// some did not, but the runner double-checks defensively so a stale
		// template revision can't drop us into a dangling-holder state.
		leafIDs := resolveLeafIDs(tmpl, instanceNodes, leafTypes, acq.NodeType, storeName)
		if len(leafIDs) == 0 {
			continue
		}
		onCommit, onGiveUp := holderActionsFor(acq.NodeDef, storeName)
		// Defaults bake into the row when the source NodeStoreRef left
		// the override empty. The spec §7.4 / §5.6.3 says a missing
		// per-resolution override should fall back to the store's
		// configured default; we resolve that here from the store
		// registry so the persisted row carries the action verbatim
		// (the §5.6.4 algorithm reads `on_commit` / `on_give_up`
		// directly without re-consulting the store).
		if onCommit == "" || onGiveUp == "" {
			d1, d2 := storeDefaultActions(args, storeName)
			if onCommit == "" {
				onCommit = d1
			}
			if onGiveUp == "" {
				onGiveUp = d2
			}
		}
		for _, leafID := range leafIDs {
			frameIDPtr := acq.FrameID
			input := storage.ClaimHolderInsertInput{
				ID:           uuid.New(),
				ClaimID:      lk.ClaimResult.ClaimID,
				StoreName:    storeName,
				HolderNodeID: leafID,
				OnCommit:     onCommit,
				OnGiveUp:     onGiveUp,
				FrameID:      &frameIDPtr,
			}
			if err := args.Storage.ClaimHolders().Insert(ctx, input, stx); err != nil {
				return fmt.Errorf("insertHeldClaimHolders: %w", err)
			}
		}
	}
	return nil
}

// resolveDeclaredClaimHolders walks acq.NodeDef.ClaimResolutions and for
// each entry runs the §5.6.4 resolution algorithm against every
// rimsky_claim_holders row keyed by (this terminal node, store_name).
// The terminal outcome is supplied by the caller (commit / give_up).
//
// Runs inside the supplied tx (wrapped via store.WithTx for
// claimstorepg's TxFromContext lookup). Multiple holder rows can match
// when a single store yielded multiple claims to the same upstream
// chain (rare; supported for completeness).
func resolveDeclaredClaimHolders(
	ctx context.Context, args RunArgs, tx pgx.Tx, acq *acquisition,
	terminal claimstorepg.TerminalOutcome,
) error {
	if acq.NodeDef == nil || len(acq.NodeDef.ClaimResolutions) == 0 {
		return nil
	}
	storeCtx := store.WithTx(ctx, tx)
	for _, r := range acq.NodeDef.ClaimResolutions {
		s, ok := args.StoreRegistry.GetStore(r.Store)
		if !ok {
			return fmt.Errorf("resolveDeclaredClaimHolders: unknown store %q", r.Store)
		}
		cs, ok := s.(*claimstorepg.Store)
		if !ok {
			// Direct-mode stores don't have held-claim semantics; skip.
			continue
		}
		ids, err := selectActiveHolderClaimIDs(ctx, tx, acq.NodeID, r.Store)
		if err != nil {
			return fmt.Errorf("resolveDeclaredClaimHolders: %w", err)
		}
		for _, claimID := range ids {
			if err := cs.ResolveOnTerminal(storeCtx, claimID, acq.NodeID.String(), terminal); err != nil {
				return fmt.Errorf("resolveDeclaredClaimHolders: %w", err)
			}
		}
	}
	return nil
}

// selectActiveHolderClaimIDs returns every claim_id keyed by the
// (holder_node_id, store_name) pair where state='active'. The §5.6.4
// algorithm walks one (claim_id, holder_node_id) pair at a time;
// returning a slice keeps the resolver loop in resolveDeclaredClaimHolders
// independent of the SQL.
func selectActiveHolderClaimIDs(
	ctx context.Context, tx pgx.Tx, holderNodeID shared.UUID, storeName string,
) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT claim_id FROM rimsky_claim_holders
		  WHERE holder_node_id = $1 AND store_name = $2 AND state = 'active'`,
		holderNodeID, storeName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// loadTemplateAndInstanceNodes pulls the instance's owning template spec
// plus every node row in the instance. Returned together because the
// caller needs both for the template-level §11.4 walk and the
// per-instance node-id resolution. Either return value may be nil
// (instance/template missing — degraded-but-tolerated).
func loadTemplateAndInstanceNodes(
	ctx context.Context, args RunArgs, instanceID shared.UUID,
) (*storage.TemplateRow, []storage.NodeRow, error) {
	inst, err := args.Storage.Instances().Get(ctx, instanceID, nil)
	if err != nil || inst == nil {
		return nil, nil, err
	}
	tmpl, err := args.Storage.Templates().Get(ctx, inst.TemplateID, nil)
	if err != nil || tmpl == nil {
		return nil, nil, err
	}
	nodes, err := args.Storage.Nodes().ListByInstance(ctx, instanceID, nil)
	if err != nil {
		return tmpl, nil, err
	}
	return tmpl, nodes, nil
}

// resolveLeafIDs maps the template-level leaf type names to per-instance
// node IDs, retaining only leaves whose template node carries a matching
// claim_resolutions entry for (source, storeName). Defensive double-
// check against the template-deploy validator (which should have
// rejected non-resolving leaves already).
func resolveLeafIDs(
	tmpl *storage.TemplateRow, instanceNodes []storage.NodeRow,
	leafTypes []string, sourceType, storeName string,
) []shared.UUID {
	resolves := make(map[string]struct{}, len(leafTypes))
	for _, leafType := range leafTypes {
		for _, tn := range tmpl.Spec.Nodes {
			if tn.Type != leafType {
				continue
			}
			for _, cr := range tn.ClaimResolutions {
				if cr.Source == sourceType && cr.Store == storeName {
					resolves[leafType] = struct{}{}
					break
				}
			}
		}
	}
	out := make([]shared.UUID, 0, len(resolves))
	for _, n := range instanceNodes {
		if _, ok := resolves[n.NodeType]; ok {
			out = append(out, n.ID)
		}
	}
	return out
}

// holderActionsFor pulls the (on_commit, on_give_up) the source's
// NodeStoreRef declares for storeName. Empty strings indicate
// "use store default" — the caller resolves them via storeDefaultActions.
func holderActionsFor(
	def *node.TemplateNodeDef, storeName string,
) (storage.ClaimHolderAction, storage.ClaimHolderAction) {
	for _, s := range def.Stores {
		if s.Name != storeName {
			continue
		}
		return storage.ClaimHolderAction(s.OnCommit),
			storage.ClaimHolderAction(s.OnGiveUp)
	}
	return "", ""
}

// storeDefaultActions resolves the configured (on_commit_default,
// on_give_up_default) for a postgres claim_store registered under
// storeName. Returns ("", "") for unknown stores or non-claim stores;
// the caller falls back to the empty string which the persisted holder
// row treats as "use store default at resolution time".
func storeDefaultActions(args RunArgs, storeName string) (storage.ClaimHolderAction, storage.ClaimHolderAction) {
	s, ok := args.StoreRegistry.GetStore(storeName)
	if !ok {
		return "", ""
	}
	cs, ok := s.(*claimstorepg.Store)
	if !ok {
		return "", ""
	}
	return storage.ClaimHolderAction(cs.OnCommitDefault()),
		storage.ClaimHolderAction(cs.OnGiveUpDefault())
}
