---
audit: template-fan-out
artifact: story:template-fan-out
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:15Z
---

# A declared fan-out node dispatches one concurrent work unit per sub-claim and the parent settles once all resolve

Supported. `TestTemplateFanOut_HappyPath_AllSuccess` (`test/scenarios/template_fan_out_e2e_test.go`) declares a fan-out node against a three-key partition request, asserts three sub-claim rows and three `work_started` events are materialized, asserts the three dispatches' start timestamps spread under 500ms (ruling out serialized dispatch), then releases the three held clones one at a time and asserts after each of the first two that the parent node has settled to neither `fresh` nor `failed`, only reaching `fresh` after the third and final release — directly proving the "settles once all sub-claims resolve" clause, not just dispatch. `TestTemplateFanOut_AbandonPropagatesToParentError` in the same file covers the failure-aggregation side of the same declaration.
