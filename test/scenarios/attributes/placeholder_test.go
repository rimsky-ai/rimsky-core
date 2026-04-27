// Placeholder package for the attributes scenario suite under
// stores-redesign-v2.
//
// The pre-v2 tests in this directory exercised
// node.NodeStoreRef.{Write, Claim, Hold} on the template DSL and the
// attributes.ResolveContext shape that consumed them. Under
// stores-redesign-v2 stores carry (Selector, Intent, Alias) and the
// substitution paths gained claim.<alias>.address /
// claim.<alias>.region alongside claim.<alias>.payload.<f>. New
// scenarios for substitution + commit-time validation + resumable-
// preserve behaviour belong here but must reflect the new substitution
// shape (see core/attributes/substitution.go).

package attributes
