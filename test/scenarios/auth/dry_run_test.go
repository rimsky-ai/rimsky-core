// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Dry-run end-to-end scenarios per spec section "Dry-run mode" and
// plan L3. Exercises:
//
//   - Key with `mode: dry_run` against a write action returns the
//     synthetic envelope and does not mutate state.
//   - Audit row has `executed: false`.
//   - Auth mutations are NOT dry-runnable (K12); request mints a
//     real key even when the grant carries `mode: dry_run`.
//
// @concept: dry-run

package auth_test

import (
	"context"
	"testing"

	"github.com/fallguy/rimsky/foundation/auth"
	"github.com/fallguy/rimsky/foundation/persistence"
)

func TestDryRun_AuthCreateIsNotDryRunnable(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	// Mint a key in anonymous mode whose grant carries dry_run on
	// auth:create (server tolerates this; handler ignores it per K12).
	code, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name": "dryrunner",
		"permissions": []map[string]any{
			{"action": "auth:create", "mode": "dry_run"},
			{"action": "auth:read"},
		},
	})
	if code != 201 {
		t.Fatalf("mint dryrunner: %d %+v", code, body)
	}
	key := body["plaintext"].(string)

	// Use it to mint another key — server should execute (not
	// dry-run) because auth mutations ignore mode.
	code, body = f.request(t, "POST", "/auth/keys", key, map[string]any{
		"name":        "real-mint",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if code != 201 {
		t.Fatalf("mint real-mint: %d %+v", code, body)
	}
	if _, ok := body["plaintext"].(string); !ok {
		t.Fatalf("real-mint missing plaintext: %+v", body)
	}
	// The new row should exist.
	code, _ = f.request(t, "GET", "/auth/keys/real-mint", key, nil)
	if code != 200 {
		t.Fatalf("real-mint not persisted: %d", code)
	}
}

// TestDryRun_AuthRevokeIsNotDryRunnable covers the second half of K12:
// `auth:revoke` ignores ModeFromContext, so a grant entry with
// `mode: dry_run` for auth:revoke must still execute the revocation.
// Verified by issuing the revoke and confirming the target key is
// actually revoked (subsequent use returns 401 / GET shows revoked_at).
func TestDryRun_AuthRevokeIsNotDryRunnable(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Mint admin so the deployment exits anonymous mode and we can
	// authoritatively check downstream auth state.
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	// Mint a "dry-run revoker": grant has auth:revoke with mode=dry_run
	// (server tolerates this; handler ignores it per K12) plus auth:read
	// so the key can verify the revocation took effect.
	_, dryBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name": "dry-revoker",
		"permissions": []map[string]any{
			{"action": "auth:revoke", "mode": "dry_run"},
			{"action": "auth:read"},
		},
	})
	dryRevoker := dryBody["plaintext"].(string)

	// Mint a target key for the dry-revoker to revoke.
	_, tgtBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "victim",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	victimKey := tgtBody["plaintext"].(string)

	// Invoke DELETE /auth/keys/victim using the dry-run-shaped grant.
	// If the handler honored mode=dry_run it would surface the synthetic
	// envelope and leave the row untouched; per K12 the handler ignores
	// mode and the revoke runs for real.
	code, resp := f.request(t, "DELETE", "/auth/keys/victim", dryRevoker, nil)
	if code != 200 {
		t.Fatalf("revoke via dry-run-shaped grant: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); dr {
		t.Fatalf("revoke returned dry_run envelope despite K12 ignoring mode: %+v", resp)
	}

	// The victim key must now be unusable: a follow-up request with it
	// returns 401 (revoked_token denial) rather than 200.
	code, _ = f.request(t, "GET", "/auth/keys", victimKey, nil)
	if code != 401 {
		t.Fatalf("victim key after dry-run-shaped revoke: %d (want 401 — revoke must have executed)", code)
	}
}

// TestDryRun_AuthRotateIsNotDryRunnable covers the third K12 case:
// `auth:rotate` ignores ModeFromContext, so a grant entry with
// `mode: dry_run` for auth:rotate must still execute the rotation
// (returning a new plaintext + revoke_at).
func TestDryRun_AuthRotateIsNotDryRunnable(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Mint admin so the deployment is no longer anonymous and we can
	// authoritatively check follow-up auth requests.
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	// Mint a "dry-run rotator": grant has auth:rotate with
	// mode=dry_run, plus auth:read for the verification follow-up.
	_, dryBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name": "dry-rotator",
		"permissions": []map[string]any{
			{"action": "auth:rotate", "mode": "dry_run"},
			{"action": "auth:read"},
		},
	})
	dryRotator := dryBody["plaintext"].(string)

	// Mint a rotation target.
	_, tgtBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "rotates-me",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	oldKey := tgtBody["plaintext"].(string)

	// Issue the rotation via the dry-run-shaped grant. Per K12 the
	// handler ignores mode; the call must run for real and surface a
	// new plaintext + revoke_at.
	code, resp := f.request(t, "POST", "/auth/keys/rotates-me/rotate", dryRotator,
		map[string]any{"grace": "1m"})
	if code != 200 {
		t.Fatalf("rotate via dry-run-shaped grant: %d %+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); dr {
		t.Fatalf("rotate returned dry_run envelope despite K12 ignoring mode: %+v", resp)
	}
	newKey, _ := resp["plaintext"].(string)
	if newKey == "" {
		t.Fatalf("rotate response missing plaintext (handler must have executed): %+v", resp)
	}
	if newKey == oldKey {
		t.Fatalf("rotate must mint a new plaintext; got identical to old key")
	}
	if revokeAt, _ := resp["revoke_at"].(string); revokeAt == "" {
		t.Fatalf("rotate response missing revoke_at: %+v", resp)
	}

	// Both keys work during grace; the new key especially must be
	// usable, which is observable only if the rotation actually ran.
	if code, _ := f.request(t, "GET", "/auth/keys", newKey, nil); code != 200 {
		t.Fatalf("new key after dry-run-shaped rotate: %d (want 200 — rotate must have executed)", code)
	}
}

func TestDryRun_NodeInvalidateReturnsSynthetic(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	// Mint admin.
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Mint a dry-run key for node:invalidate.
	_, body = f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name": "dry-invalidate",
		"permissions": []map[string]any{
			{"action": "node:invalidate", "mode": "dry_run"},
		},
	})
	dryKey := body["plaintext"].(string)
	// Seed a real node so the handler reaches the dry-run gate (which
	// fires AFTER validation per spec section "Dry-run mode").
	nodeID := seedDryRunNode(t, f, adminKey)
	code, resp := f.request(t, "POST", "/nodes/"+nodeID+"/invalidate", dryKey, map[string]any{
		"reason": "test",
	})
	if code != 200 {
		t.Fatalf("dry-run invalidate: code=%d resp=%+v", code, resp)
	}
	if dr, _ := resp["dry_run"].(bool); !dr {
		t.Fatalf("dry-run envelope missing: %+v", resp)
	}

	// Audit row for the attempt should exist with mode=dry_run +
	// executed=false. List events of kind=auth.access_attempted.
	// Flush the audit dispatcher so the inserts are visible.
	f.flushAudit()
	ctx := context.Background()
	var foundDryRun int
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessAttempted}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			if mode, _ := e.Payload["mode"].(string); mode == string(auth.ModeDryRun) {
				foundDryRun++
				if exec, _ := e.Payload["executed"].(bool); exec {
					t.Errorf("dry_run row has executed=true: %+v", e.Payload)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if foundDryRun == 0 {
		t.Fatalf("expected at least one dry_run audit row")
	}
}

// seedDryRunNode registers a single-node template, deploys it, and
// creates an instance — returning a real node id the dry-run test
// can target. The node-invalidate handler only reaches its dry-run
// gate after the node lookup succeeds, so a placeholder UUID would
// short-circuit at the 404 path and never exercise the dry-run code.
//
// Uses the supplied admin key for the (authenticated) seeding
// requests; the caller's test already minted that key.
func seedDryRunNode(t *testing.T, f *authFixture, adminKey string) string {
	t.Helper()
	// Register a minimal template. `name`, `version`, and
	// `frame_resolution_mode` are required by the template
	// validator; the node executor is left empty so the validator's
	// `ExecutorDeclared` hook (nil in this fixture) doesn't reject
	// it.
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":                  "dry-run-seed",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes":                 []map[string]any{{"type": "n1"}},
		},
	}
	code, regResp := f.request(t, "POST", "/templates", adminKey, tplBody)
	if code != 201 && code != 200 {
		t.Fatalf("seed template register: %d %+v", code, regResp)
	}
	hash, _ := regResp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seed template register missing template_id: %+v", regResp)
	}
	code, depResp := f.request(t, "POST", "/templates/"+hash+"/deploy", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seed template deploy: %d %+v", code, depResp)
	}
	code, instResp := f.request(t, "POST", "/instances", adminKey, map[string]any{"template": hash})
	if code != 201 && code != 200 {
		t.Fatalf("seed instance create: %d %+v", code, instResp)
	}
	instID, _ := instResp["instance_id"].(string)
	if instID == "" {
		t.Fatalf("seed instance missing id: %+v", instResp)
	}
	// List the instance's nodes; the first node is the one we just
	// created.
	code, nodesResp := f.request(t, "GET", "/instances/"+instID+"/nodes", adminKey, nil)
	if code != 200 {
		t.Fatalf("seed list nodes: %d %+v", code, nodesResp)
	}
	nodes, _ := nodesResp["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatalf("seed instance has no nodes: %+v", nodesResp)
	}
	n0, _ := nodes[0].(map[string]any)
	if id, _ := n0["id"].(string); id != "" {
		return id
	}
	t.Fatalf("seed node missing id: %+v", nodes[0])
	return ""
}
