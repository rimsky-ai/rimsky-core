// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Placeholder package for the attributes scenario suite under the
// stores redesign.
//
// Pre-redesign tests in this directory exercised
// node.NodeStoreRef.{Write, Claim, Hold} on the template DSL and the
// attributes.ResolveContext shape that consumed them. Under the
// redesign stores carry (Selector, Intent, Alias) and the
// substitution paths gained claim.<alias>.address /
// claim.<alias>.scope alongside claim.<alias>.payload.<f>. New
// scenarios for substitution + commit-time validation + resumable-
// preserve behaviour belong here but must reflect the new
// substitution shape (see core/attributes/substitution.go).

package attributes
