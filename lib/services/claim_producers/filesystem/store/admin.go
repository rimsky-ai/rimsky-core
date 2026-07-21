// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/sync/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rawSelector := strings.TrimPrefix(r.URL.Path, "/admin/sync/")
		selector, err := url.PathUnescape(rawSelector)
		if err != nil {
			http.Error(w, "selector not valid percent-encoding: "+err.Error(), http.StatusBadRequest)
			return
		}
		if selector == "" {
			http.Error(w, "selector is required", http.StatusBadRequest)
			return
		}
		pp, ok := s.pickPolicies[selector]
		if !ok {
			http.Error(w, fmt.Sprintf("unknown selector %q", selector), http.StatusBadRequest)
			return
		}
		if err := s.runSync(selector, pp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/admin/bump-to-head/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rawSelector := strings.TrimPrefix(r.URL.Path, "/admin/bump-to-head/")
		selector, err := url.PathUnescape(rawSelector)
		if err != nil {
			http.Error(w, "selector not valid percent-encoding: "+err.Error(), http.StatusBadRequest)
			return
		}
		if selector == "" {
			http.Error(w, "selector is required", http.StatusBadRequest)
			return
		}
		pp, ok := s.pickPolicies[selector]
		if !ok {
			http.Error(w, fmt.Sprintf("unknown selector %q", selector), http.StatusBadRequest)
			return
		}
		var body struct {
			Folder string `json:"folder"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		folder := body.Folder
		if folder == "" {
			http.Error(w, "folder is required", http.StatusBadRequest)
			return
		}
		if strings.HasPrefix(folder, ".") {
			http.Error(w, "folder must not start with '.'", http.StatusBadRequest)
			return
		}
		if strings.ContainsAny(folder, `/\`) {
			http.Error(w, "folder must not contain path separators", http.StatusBadRequest)
			return
		}
		if folder == "." || folder == ".." || folder != filepath.Clean(folder) {
			http.Error(w, "folder must be a single path component", http.StatusBadRequest)
			return
		}
		if pp.FolderPattern != nil && !pp.FolderPattern.MatchString(folder) {
			http.Error(w, "folder violates configured pattern", http.StatusBadRequest)
			return
		}
		folderAbs := filepath.Join(s.root, pp.Root, folder)
		info, err := os.Stat(folderAbs)
		if err != nil || !info.IsDir() {
			http.Error(w, "folder not found", http.StatusNotFound)
			return
		}
		availSentinel := filepath.Join(PolicyStateDir(s.root, selector), "available", folder)
		if err := stampRingPosition(availSentinel, ringPositionHead); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if folderInProgress(s.root, selector, folder) {
					http.Error(w, "folder is in_progress", http.StatusConflict)
					return
				}
				http.Error(w, "folder not in queue (sync may not have enqueued it yet)", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func folderInProgress(storeRoot, selector, folder string) bool {
	dir := filepath.Join(PolicyStateDir(storeRoot, selector), "in_progress")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		f, _, _, perr := ParseFromRight(e.Name())
		if perr == nil && f == folder {
			return true
		}
	}
	return false
}
