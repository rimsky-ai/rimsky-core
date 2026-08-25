// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

func PageAll[T any](fetch func(cursor string) ([]T, string, error)) ([]T, error) {
	var all []T
	cursor := ""
	for {
		rows, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
		if next == "" || next == cursor || len(rows) == 0 {
			return all, nil
		}
		cursor = next
	}
}
