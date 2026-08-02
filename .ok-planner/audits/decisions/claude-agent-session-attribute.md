---
audit: claude-agent-session-attribute
artifact: decision:claude-agent-session-attribute
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:41Z
---

# Session token rides the attribute delta on Success and Park scratch on Park, scratch-first on read, fresh in a sub-graph

Supported. `agentrun.go`'s `OnComplete` handler writes `effectiveBag["session_token"] = opts.SessionID` into the Success attribute delta, and its `OnPark`/rate-limit paths set `AgentOutcome.SessionToken = opts.SessionID`, which `requestparse.go::OutcomeToCallbackBody` encodes into the callback's `park.scratch` field via `SessionTokenToScratchBase64` — never into an attribute delta on that leg, matching the decision's channel split. `server.go::sessionTokenOr` (tagged with this decision) reads the decoded scratch value first and falls back to `attributes["session_token"]` only when scratch is empty, feeding the resolved value into `AgentRunOptions.SessionToken`, which `agentrun.go` uses to choose `CliRunner.Resume` over `Spawn`. Each leg is independently tested: the Success-leg attribute path and the sub-graph reset (empty scratch, empty carried attribute, hence `Spawn` not `Resume`, and no recalled conversational fact) are proven by a three-turn-plus-sub-graph end-to-end scenario test; the Park leg's `SessionToken` assignment is unit-tested (`TestRunAgentRateLimitParksByDefault`, `TestRunAgentBlockedAndParkViaMcp`); the scratch encode/decode round-trip is unit-tested (`TestSessionTokenScratchRoundTrip`); and the resume trigger off a resolved token is unit-tested (`TestRunAgentSessionTokenTriggersResumePath`). No mechanism anywhere in the package carries the token across a frame boundary other than the generic message-borne path the decision points to.
