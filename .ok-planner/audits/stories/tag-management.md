---
audit: tag-management
artifact: story:tag-management
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:40Z
---

# Operator manages movable template-hash names

Supported. `lib/control/controlapi/tags.go::registerTagsRoutes` wires all four capabilities the story names: `POST /v1/tags` (create, rejecting a hash-shaped or duplicate name), `GET /v1/tags` (list), `PUT /v1/tags/{tag}` (rebind to a new template hash), and `DELETE /v1/tags/{tag}` (remove); a tag also resolves as a `template` value at instance-create and template-deploy time via `resolveTagOrHash`. `lib/control/controlapi/tags_test.go` covers create, list, rebind, and delete including 404-on-missing and duplicate/invalid-name rejection. `test/scenarios/tag_management_e2e_test.go` (carrying the story's citation) drives the full round trip end to end: creates a tag bound to one template hash, creates an instance against the tag name and confirms it resolves to that hash, rebinds the tag to a second template hash via `PUT`, confirms a fresh instance against the same tag name now resolves to the new hash while the original (pre-rebind) instance still references the old one, deletes the tag, and confirms a post-delete instance-create against the now-gone tag name is refused with a 4xx.
