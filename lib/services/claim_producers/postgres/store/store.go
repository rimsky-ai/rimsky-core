// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

var ItemsTableIdentRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type Store struct {
	pool              *pgxpool.Pool
	writeSemantics    claimproducer.WriteSemantics
	pickPolicies      map[string]*PickPolicy
	partitionPolicies map[string]*PartitionPolicy
	ledger            *ClaimLedger
}

func (s *Store) Ledger() *ClaimLedger { return s.ledger }

func NewForTest() *Store {
	return &Store{ledger: NewClaimLedger(1024)}
}

func (s *Store) SetPoolForTest(pool *pgxpool.Pool) {
	s.pool = pool
}

func (s *Store) SetPartitionPoliciesForTest(policies map[string]*PartitionPolicy) {
	s.partitionPolicies = policies
}

func (s *Store) SetPickPoliciesForTest(policies map[string]*PickPolicy) {
	s.pickPolicies = policies
}

type PickPolicy struct {
	ItemsTable        string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
}

// @concept: fan-out
type PartitionPolicy struct {
	ItemsTable string
	Select     string
	Where      string
	ParamOrder []string
	Limit      int
}

type Config struct {
	Connection        string
	WriteSemantics    claimproducer.WriteSemantics
	PickPolicies      map[string]*PickPolicy
	PartitionPolicies map[string]*PartitionPolicy
	LedgerMaxRecords  int
}

const defaultLedgerMaxRecords = 1024

func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Connection == "" {
		return nil, errors.New("postgres store: connection is required")
	}
	pool, err := pgxpool.New(ctx, cfg.Connection)
	if err != nil {
		return nil, fmt.Errorf("postgres store: open pool: %w", err)
	}
	if cfg.WriteSemantics == "" {
		cfg.WriteSemantics = claimproducer.WriteSemanticsStagedAsync
	}
	for selector, pp := range cfg.PickPolicies {
		res := validatePickPolicy(selector, pp)
		if !res.OK() {
			pool.Close()
			msgs := make([]string, 0, len(res.Errors))
			for _, e := range res.Errors {
				msgs = append(msgs, e.Error())
			}
			return nil, fmt.Errorf("postgres store: pick_policies[%q]: %s",
				selector, strings.Join(msgs, "; "))
		}
		for _, w := range res.Warnings {
			slog.Warn(w)
		}
		if err := verifyItemsTable(ctx, pool, pp.ItemsTable); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres store: pick_policies[%q]: items table %q: %w",
				selector, pp.ItemsTable, err)
		}
	}
	for name, pp := range cfg.PartitionPolicies {
		if err := validatePartitionPolicy(name, pp); err != nil {
			pool.Close()
			return nil, err
		}
	}
	ledgerMax := cfg.LedgerMaxRecords
	if ledgerMax <= 0 {
		ledgerMax = defaultLedgerMaxRecords
	}
	return &Store{
		pool:              pool,
		writeSemantics:    cfg.WriteSemantics,
		pickPolicies:      cfg.PickPolicies,
		partitionPolicies: cfg.PartitionPolicies,
		ledger:            NewClaimLedger(ledgerMax),
	}, nil
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) Capabilities() claimproducer.Capabilities {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{s.writeSemantics},
	}
}

func (s *Store) PickPolicies() map[string]PickPolicy {
	out := make(map[string]PickPolicy, len(s.pickPolicies))
	for sel, pp := range s.pickPolicies {
		out[sel] = *pp
	}
	return out
}

func (s *Store) PartitionPolicies() map[string]PartitionPolicy {
	out := make(map[string]PartitionPolicy, len(s.partitionPolicies))
	for name, pp := range s.partitionPolicies {
		out[name] = *pp
	}
	return out
}

func (s *Store) PartitionPolicy(name string) (*PartitionPolicy, bool) {
	pp, ok := s.partitionPolicies[name]
	if !ok {
		return nil, false
	}
	return pp, true
}

type ClaimLookup struct {
	ClaimID  string
	Selector string
	Address  []byte
	Scope    []byte
	IsOpen   bool
}

func (s *Store) LookupClaim(claimID string) (ClaimLookup, bool) {
	rec, ok := s.ledger.Get(claimID)
	if !ok {
		return ClaimLookup{}, false
	}
	return ClaimLookup{
		ClaimID:  rec.ClaimID,
		Selector: rec.Selector,
		Address:  rec.Address,
		Scope:    rec.Scope,
		IsOpen:   rec.State == ClaimStateOpen,
	}, true
}

func (s *Store) Open(ctx context.Context, claimID, selector string, intent claimproducer.Intent) (claimproducer.OpenOutcome, error) {
	if pp, ok := s.pickPolicies[selector]; ok {
		out, err := s.openPickPolicy(ctx, claimID, pp)
		if err == nil && out.Available {
			s.ledger.RecordOpen(claimID, selector, out.Result.Address, out.Result.ClaimScope)
		}
		return out, err
	}
	if s.writeSemantics == claimproducer.WriteSemanticsStagedAsync && intent != claimproducer.IntentRead {
		if !schemaIdentRegex.MatchString(selector) {
			return claimproducer.OpenOutcome{}, fmt.Errorf(
				"postgres store: staged_async write on selector %q requires a valid schema identifier "+
					"(lowercase letters/digits/underscore; not starting with a digit); rejecting rather than "+
					"silently realizing an in-place write while reporting staged_async coexistence", selector)
		}
		addr, scope, err := s.openStaging(ctx, claimID, selector)
		if err != nil {
			return claimproducer.OpenOutcome{}, err
		}
		s.ledger.RecordOpen(claimID, selector, addr, scope)
		return claimproducer.OpenOutcome{
			Available: true,
			Result: claimproducer.ClaimResult{
				Address:                addr,
				ClaimScope:             scope,
				RealizedWriteSemantics: s.writeSemantics,
			},
		}, nil
	}

	addr, err := json.Marshal(selector)
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: marshal selector: %w", err)
	}
	s.ledger.RecordOpen(claimID, selector, addr, addr)
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(addr),
			ClaimScope:             json.RawMessage(addr),
			RealizedWriteSemantics: s.writeSemantics,
		},
	}, nil
}

func (s *Store) openPickPolicy(ctx context.Context, claimID string, pp *PickPolicy) (claimproducer.OpenOutcome, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := fmt.Sprintf(`UPDATE %s
		   SET state = 'in_progress', claim_token = $1, claimed_at = now()
		 WHERE item_id = (
		       SELECT item_id FROM %s
		        WHERE state = 'available'
		        ORDER BY priority DESC, sequence ASC
		          FOR UPDATE SKIP LOCKED
		        LIMIT 1
		       )
		 RETURNING item_id, payload`,
		pp.ItemsTable, pp.ItemsTable,
	)
	row := tx.QueryRow(ctx, q, claimID)
	var (
		itemID  string
		rawJSON []byte
	)
	if err := row.Scan(&itemID, &rawJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return claimproducer.OpenOutcome{Available: false, UnavailableClass: ClaimUnavailableClass}, nil
		}
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: pick: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: commit pick tx: %w", err)
	}

	addrBytes, _ := json.Marshal(itemID)
	scopeBytes, _ := json.Marshal(itemID)

	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(addrBytes),
			Payload:                rawJSON,
			ClaimScope:             json.RawMessage(scopeBytes),
			RealizedWriteSemantics: s.writeSemantics,
		},
	}, nil
}

func (s *Store) Commit(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, canonical, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		if err := s.commitStagingSwap(ctx, canonical, staging); err != nil {
			s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
		s.ledger.RecordTerminal(claimID, "claim_committed", nil)
		return nil
	}
	if err := s.applyPickAction(ctx, claimID, true); err != nil {
		s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	s.ledger.RecordTerminal(claimID, "claim_committed", nil)
	return nil
}

func (s *Store) Abandon(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, _, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		if err := s.dropStaging(ctx, staging); err != nil {
			s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
		s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
		return nil
	}
	if err := s.applyPickAction(ctx, claimID, false); err != nil {
		s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
	return nil
}

func (s *Store) Release(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, _, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_release_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		if err := s.dropStaging(ctx, staging); err != nil {
			s.ledger.RecordEvent(claimID, "claim_release_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
	}
	s.ledger.RecordTerminal(claimID, "claim_released", nil)
	return nil
}

func (s *Store) stagedSwapTarget(claimScope, address []byte) (swap bool, canonical, staging string, err error) {
	if s.writeSemantics != claimproducer.WriteSemanticsStagedAsync {
		return false, "", "", nil
	}
	if !isJSONStringShape(claimScope) || !isJSONStringShape(address) {
		return false, "", "", nil
	}
	canonical, err = decodeSchemaName(claimScope)
	if err != nil {
		return false, "", "", err
	}
	staging, err = decodeSchemaName(address)
	if err != nil {
		return false, "", "", err
	}
	if canonical == "" || staging == "" || canonical == staging {
		return false, "", "", nil
	}
	if _, ok := s.pickPolicies[canonical]; ok {
		return false, "", "", nil
	}
	if !schemaIdentRegex.MatchString(canonical) {
		return false, "", "", nil
	}
	return true, canonical, staging, nil
}

func isJSONStringShape(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			return true
		default:
			return false
		}
	}
	return false
}

func (s *Store) applyPickAction(ctx context.Context, claimID string, successPath bool) error {
	if claimID == "" {
		return nil
	}
	pp, found, err := s.findPolicyForClaim(ctx, claimID)
	if err != nil {
		return fmt.Errorf("postgres store: locate policy for claim: %w", err)
	}
	if !found {
		return nil
	}
	var act action.Action
	if successPath {
		act = pp.OnCommit
	} else {
		act = pp.OnGiveUp
	}
	if !validPickAction(act.Kind) {
		return fmt.Errorf("postgres store: applyPickAction: invalid action %q", act.Kind)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres store: begin action tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	switch act.Kind {
	case action.Pop:
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE claim_token = $1`, pp.ItemsTable), claimID,
		); err != nil {
			return fmt.Errorf("postgres store: pop item: %w", err)
		}
	case action.Recycle:
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL,
			        sequence = nextval(pg_get_serial_sequence($1, 'sequence'))
			  WHERE claim_token = $2`, pp.ItemsTable),
			pp.ItemsTable, claimID,
		); err != nil {
			return fmt.Errorf("postgres store: recycle: %w", err)
		}
	default:
		return fmt.Errorf("postgres store: applyPickAction: action %q not supported by postgres store", act.Kind)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres store: commit action tx: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) findPolicyForClaim(ctx context.Context, claimID string) (*PickPolicy, bool, error) {
	for _, pp := range s.pickPolicies {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE claim_token = $1)`, pp.ItemsTable)
		if err := s.pool.QueryRow(ctx, query, claimID).Scan(&exists); err != nil {
			return nil, false, fmt.Errorf("query items_table %q: %w", pp.ItemsTable, err)
		}
		if exists {
			return pp, true, nil
		}
	}
	return nil, false, nil
}

func (s *Store) InsertItems(ctx context.Context, selector string, payloads []json.RawMessage) error {
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return fmt.Errorf("postgres store: InsertItems: no pick policy for selector %q", selector)
	}
	if len(payloads) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(
		`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`,
		pp.ItemsTable,
	)
	for i, p := range payloads {
		if len(p) == 0 {
			return fmt.Errorf("postgres store: InsertItems: payload at index %d is empty", i)
		}
		if !json.Valid(p) {
			return fmt.Errorf("postgres store: InsertItems: payload at index %d is not valid JSON", i)
		}
		if _, err := s.pool.Exec(ctx, stmt, uuid.NewString(), []byte(p)); err != nil {
			return fmt.Errorf("postgres store: InsertItems: row %d: %w", i, err)
		}
	}
	return nil
}

func validPickAction(k action.Kind) bool {
	return k == action.Pop || k == action.Recycle
}

func validIdent(s string) bool {
	return ItemsTableIdentRegex.MatchString(s)
}

func validatePickPolicy(selector string, pp *PickPolicy) action.ValidationResult {
	var res action.ValidationResult
	addErr := func(err error) { res.Errors = append(res.Errors, err) }

	if pp == nil {
		addErr(errors.New("policy is nil"))
		return res
	}

	if !validIdent(pp.ItemsTable) {
		addErr(fmt.Errorf("items_table %q is not a valid identifier (lowercase letters/digits/underscore; not starting with a digit)", pp.ItemsTable))
	}

	commitHandled := pgZeroOrRejected("on_commit", pp.OnCommit, addErr)
	giveUpHandled := pgZeroOrRejected("on_give_up", pp.OnGiveUp, addErr)

	if !commitHandled {
		if err := pp.OnCommit.Validate(); err != nil {
			addErr(fmt.Errorf("on_commit: %w", err))
		}
	}
	if !giveUpHandled {
		if err := pp.OnGiveUp.Validate(); err != nil {
			addErr(fmt.Errorf("on_give_up: %w", err))
		}
	}

	if pp.VisibilityTimeout <= 0 {
		addErr(errors.New("visibility_timeout_seconds: must be > 0"))
	}

	_ = selector
	return res
}

var paramPlaceholderRegex = regexp.MustCompile(`\$\d+`)

func validatePartitionPolicy(name string, pp *PartitionPolicy) error {
	if pp == nil {
		return fmt.Errorf("postgres store: partition_policies[%q]: policy is nil", name)
	}
	if !validIdent(pp.ItemsTable) {
		return fmt.Errorf("postgres store: partition_policies[%q]: items_table %q is not a valid identifier (lowercase letters/digits/underscore; not starting with a digit)",
			name, pp.ItemsTable)
	}
	if strings.TrimSpace(pp.Select) == "" {
		return fmt.Errorf("postgres store: partition_policies[%q]: select must be non-empty", name)
	}
	if strings.TrimSpace(pp.Where) == "" {
		return fmt.Errorf("postgres store: partition_policies[%q]: where must be non-empty", name)
	}
	if paramPlaceholderRegex.MatchString(pp.Where) && len(pp.ParamOrder) == 0 {
		return fmt.Errorf("postgres store: partition_policies[%q]: where contains $N placeholder(s) but params_schema is missing; declare params_schema to bind placeholder ordering deterministically (alphabetical fallback would silently scramble bindings)",
			name)
	}
	if firstCol := firstSelectColumn(pp.Select); firstCol != "" && !looksLikeIDColumn(firstCol) {
		slog.Warn("postgres store: partition_policies: first selected column should be the row id and looks atypical",
			"policy", name, "first_column", firstCol,
			"hint", "the first column is used as the partition_key; non-text columns may produce inconsistent wire shapes")
	}
	return nil
}

func firstSelectColumn(sel string) string {
	trimmed := strings.TrimSpace(sel)
	if trimmed == "" || trimmed == "*" {
		return ""
	}
	commaIdx := strings.IndexByte(trimmed, ',')
	first := trimmed
	if commaIdx >= 0 {
		first = strings.TrimSpace(trimmed[:commaIdx])
	}
	if asIdx := strings.LastIndex(strings.ToLower(first), " as "); asIdx >= 0 {
		first = strings.TrimSpace(first[asIdx+4:])
	} else if spaceIdx := strings.LastIndexByte(first, ' '); spaceIdx >= 0 {
		first = strings.TrimSpace(first[spaceIdx+1:])
	}
	return first
}

func looksLikeIDColumn(col string) bool {
	c := strings.ToLower(strings.Trim(col, `"'`))
	return c == "id" || strings.HasSuffix(c, "_id") || c == "item_id" || c == "row_id" || c == "key"
}

type PolicyRow struct {
	ID      string
	RowJSON []byte
}

func (s *Store) RunPartitionPolicy(ctx context.Context, pp *PartitionPolicy, params map[string]any) ([]PolicyRow, error) {
	if pp == nil {
		return nil, fmt.Errorf("postgres store: RunPartitionPolicy: policy is nil")
	}
	order := pp.ParamOrder
	if len(order) == 0 && len(params) > 0 {
		return nil, fmt.Errorf("postgres store: RunPartitionPolicy: policy supplies %d params but params_schema (ParamOrder) is empty; declare params_schema to bind $N placeholders deterministically (alphabetical fallback would silently scramble bindings)", len(params))
	}
	args := make([]any, 0, len(order))
	for _, k := range order {
		v, ok := params[k]
		if !ok {
			return nil, fmt.Errorf("postgres store: RunPartitionPolicy: param %q declared in params_schema but missing from request", k)
		}
		args = append(args, v)
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s", pp.Select, pp.ItemsTable, pp.Where)
	if pp.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", pp.Limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres store: RunPartitionPolicy: query: %w", err)
	}
	defer rows.Close()
	fieldDescs := rows.FieldDescriptions()
	if len(fieldDescs) == 0 {
		return nil, fmt.Errorf("postgres store: RunPartitionPolicy: select list must include at least the row id column")
	}
	colNames := make([]string, len(fieldDescs))
	for i, f := range fieldDescs {
		colNames[i] = string(f.Name)
	}
	var out []PolicyRow
	seenIDs := make(map[string]struct{})
	for rows.Next() {
		vals, scanErr := rows.Values()
		if scanErr != nil {
			return nil, fmt.Errorf("postgres store: RunPartitionPolicy: scan row: %w", scanErr)
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("postgres store: RunPartitionPolicy: row returned zero columns")
		}
		id, idErr := canonicalRowID(vals[0])
		if idErr != nil {
			return nil, fmt.Errorf("postgres store: RunPartitionPolicy: canonicalize row id (col %q): %w", colNames[0], idErr)
		}
		if _, dup := seenIDs[id]; dup {
			return nil, fmt.Errorf(
				"postgres store: RunPartitionPolicy: row id %q (col %q) is returned more than once; "+
					"a partition policy's select must yield distinct ids so sub-claim scopes stay disjoint",
				id, colNames[0])
		}
		seenIDs[id] = struct{}{}
		row := make(map[string]any, len(colNames))
		for i, name := range colNames {
			row[name] = normalizePolicyValue(vals[i])
		}
		rowJSON, mErr := json.Marshal(row)
		if mErr != nil {
			return nil, fmt.Errorf("postgres store: RunPartitionPolicy: marshal row id=%q: %w", id, mErr)
		}
		out = append(out, PolicyRow{ID: id, RowJSON: rowJSON})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: RunPartitionPolicy: iterate rows: %w", err)
	}
	return out, nil
}

func canonicalRowID(v any) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case []byte:
		return string(val), nil
	case [16]byte:
		return uuid.UUID(val).String(), nil
	case pgtype.Numeric:
		return canonicalNumericString(val)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var unq string
		if uerr := json.Unmarshal(b, &unq); uerr == nil {
			return unq, nil
		}
	}
	return s, nil
}

func canonicalNumericString(n pgtype.Numeric) (string, error) {
	if !n.Valid {
		return "", fmt.Errorf("numeric row id is NULL")
	}
	dv, err := n.Value()
	if err != nil {
		return "", fmt.Errorf("encode numeric row id: %w", err)
	}
	s, ok := dv.(string)
	if !ok {
		return "", fmt.Errorf("numeric row id encoded as unexpected type %T", dv)
	}
	return s, nil
}

func normalizePolicyValue(v any) any {
	switch val := v.(type) {
	case []byte:
		if json.Valid(val) {
			return json.RawMessage(val)
		}
		return string(val)
	case [16]byte:
		return uuid.UUID(val).String()
	case pgtype.Numeric:
		s, err := canonicalNumericString(val)
		if err != nil {
			return nil
		}
		return json.Number(s)
	}
	return v
}

func pgZeroOrRejected(slot string, a action.Action, addErr func(error)) bool {
	if a.Kind == "" {
		addErr(fmt.Errorf("%s: required (got null or missing)", slot))
		return true
	}
	switch a.Kind {
	case action.PopAndMove:
		addErr(fmt.Errorf("%s: action %q not supported by postgres store; supported actions are pop and recycle", slot, a.Kind))
		return true
	case action.PopAndDelete:
		addErr(fmt.Errorf("%s: action %q not supported by postgres store (semantically equivalent to pop; use pop)", slot, a.Kind))
		return true
	}
	return false
}

var expectedColumns = []struct {
	name     string
	dataType string
}{
	{"item_id", "text"},
	{"payload", "jsonb"},
	{"state", "text"},
	{"claim_token", "text"},
	{"claimed_at", "timestamp with time zone"},
	{"enqueued_at", "timestamp with time zone"},
	{"priority", "integer"},
	{"sequence", "bigint"},
}

func verifyItemsTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	rows, err := pool.Query(ctx,
		`SELECT column_name, data_type
		   FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = $1`,
		table,
	)
	if err != nil {
		return fmt.Errorf("query information_schema.columns: %w", err)
	}
	defer rows.Close()

	got := make(map[string]string)
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			return fmt.Errorf("scan column row: %w", err)
		}
		got[col] = typ
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate column rows: %w", err)
	}
	if len(got) == 0 {
		return fmt.Errorf("table not found in current schema (or has zero columns)")
	}
	for _, want := range expectedColumns {
		gotType, ok := got[want.name]
		if !ok {
			return fmt.Errorf("missing column %q (expected type %q)", want.name, want.dataType)
		}
		if gotType != want.dataType {
			return fmt.Errorf("column %q has type %q, expected %q", want.name, gotType, want.dataType)
		}
	}
	return nil
}
