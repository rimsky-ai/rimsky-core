// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: permission

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func requestTargets(ctx context.Context, persist persistence.Tables, logger foundationshared.Logger, action string, body []byte, r *http.Request) []map[string]string {
	switch action {
	case "template:register":
		if tag := registerRequestTag(body); tag != "" {
			return []map[string]string{{"template_tag": tag}}
		}
	case "template:deploy", "template:undeploy", "template:deregister":
		return templateIDTargets(ctx, persist, logger, chi.URLParam(r, "id"))
	case "tag:set", "tag:delete":
		if tag := chi.URLParam(r, "tag"); tag != "" {
			return []map[string]string{{"template_tag": tag}}
		}
	case "instance:create":
		if tpl := instanceCreateTemplate(body); tpl != "" {
			return templateIDTargets(ctx, persist, logger, tpl)
		}
	}
	return []map[string]string{{}}
}

func templateIDTargets(ctx context.Context, persist persistence.Tables, logger foundationshared.Logger, idOrTag string) []map[string]string {
	if idOrTag == "" {
		return []map[string]string{{}}
	}
	if !looksLikeHash(idOrTag) {
		return []map[string]string{{"template_tag": idOrTag}}
	}
	if persist == nil {
		return []map[string]string{{}}
	}
	var rows []persistence.TemplateTagRow
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := persist.TemplateTags().ListByTemplate(ctx, idOrTag, tx)
		rows = r
		return err
	})
	if err != nil {
		if logger != nil {
			logger.Warn("auth.template_targets_lookup_failed", "template_id", idOrTag, "err", err.Error())
		}
		return []map[string]string{{}}
	}
	if len(rows) == 0 {
		return []map[string]string{{}}
	}
	out := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]string{"template_tag": r.Tag})
	}
	return out
}

func registerRequestTag(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Tag
}

func instanceCreateTemplate(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Template string `json:"template"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Template)
}
