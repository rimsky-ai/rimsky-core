// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (s *Store) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/items/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rawSelector := strings.TrimPrefix(r.URL.Path, "/admin/items/")
		selector, err := url.PathUnescape(rawSelector)
		if err != nil {
			http.Error(w, "selector not valid percent-encoding: "+err.Error(), http.StatusBadRequest)
			return
		}
		if selector == "" {
			http.Error(w, "selector is required", http.StatusBadRequest)
			return
		}
		var body struct {
			Items []struct {
				Payload json.RawMessage `json:"payload"`
			} `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(body.Items) == 0 {
			http.Error(w, "items array must not be empty", http.StatusBadRequest)
			return
		}
		payloads := make([]json.RawMessage, 0, len(body.Items))
		for i, item := range body.Items {
			if len(item.Payload) == 0 {
				http.Error(w, "items["+strconv.Itoa(i)+"].payload is required", http.StatusBadRequest)
				return
			}
			if !json.Valid(item.Payload) {
				http.Error(w, "items["+strconv.Itoa(i)+"].payload is not valid JSON", http.StatusBadRequest)
				return
			}
			payloads = append(payloads, item.Payload)
		}
		if err := s.InsertItems(r.Context(), selector, payloads); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]int{"inserted": len(payloads)})
	})
	return mux
}
