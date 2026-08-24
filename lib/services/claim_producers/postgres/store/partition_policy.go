// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// @concept: fan-out
type PartitionPolicy struct {
	ItemsTable string
	Select     string
	Where      string
	ParamOrder []string
	Limit      int
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
		slog.Warn("POSTGRESSTORE.PARTITIONPOLICYCOLUMNS.ATYPICAL", "detail", "the first selected column should be the row id, and this one does not look like one",
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
		return nil, &ClassedError{Class: PartitionPolicyInvalidRequestClass, Err: fmt.Errorf(
			"postgres store: RunPartitionPolicy: policy supplies %d params but params_schema (ParamOrder) is empty; declare params_schema to bind $N placeholders deterministically (alphabetical fallback would silently scramble bindings)", len(params))}
	}
	args := make([]any, 0, len(order))
	for _, k := range order {
		v, ok := params[k]
		if !ok {
			return nil, &ClassedError{Class: PartitionPolicyInvalidRequestClass, Err: fmt.Errorf(
				"postgres store: RunPartitionPolicy: param %q declared in params_schema but missing from request", k)}
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
