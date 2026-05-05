// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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
	"time"
)

// AdminHandler returns an http.Handler for the fs store's admin
// surface. v1 ships a single endpoint:
//
//	POST /admin/bump-to-head/{selector}
//	  body: {"folder": "<folder-name>"}
//	  responses: 204 | 400 | 404 | 409 | 500
//
// Selector path-param accepts the raw "@policy-name" form or its
// percent-encoded "%40policy-name" form. Mirrors pg's URL-shape
// convention (stores/postgres/store/admin.go).
func (s *Store) AdminHandler() http.Handler {
	mux := http.NewServeMux()
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
		// Reject any path-traversal shape: embedded separators, "..",
		// or anything that filepath.Clean wouldn't preserve as a single
		// component. Without this, FolderPattern-less policies could
		// accept "foo/../../etc" and have filepath.Join resolve outside
		// pp.Root. Mirrors openRegional's escape-check stance.
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
		availSentinel := filepath.Join(policyStateDir(s.root, selector), "available", folder)
		epoch := time.Unix(0, 0)
		if err := os.Chtimes(availSentinel, epoch, epoch); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// sentinel missing — distinguish "raced into in_progress" from "not enqueued yet"
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

// folderInProgress returns true if any sentinel under .fs-store/<sel>/in_progress/
// parses to <folder>. Used by bump-to-head to distinguish 409 from 404.
func folderInProgress(storeRoot, selector, folder string) bool {
	dir := filepath.Join(policyStateDir(storeRoot, selector), "in_progress")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		f, _, _, perr := parseFromRight(e.Name())
		if perr == nil && f == folder {
			return true
		}
	}
	return false
}
