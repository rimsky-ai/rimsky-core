---
assessment: forensic-last-attribute--node-read
subject: story:forensic-last-attribute
way: node-read
release: d977250c
outcome: held
warrant: experiment:forensic-last-attribute
---
# Reading a node's latest resolved attribute bag from the node read surface

On a template whose first node cascades to itself and dispatches three times — so there is a history of bags to be wrong about — `catalog:http-routes/GET /v1/nodes/{id}` answered with the third dispatch's resolved bag, not an earlier one's. The bag carried a resolved input value that no emitted delta ever contained, so it is the values the node actually computed with rather than a replay of what it emitted. No single event in the feed carries that bag: an operator working from `catalog:http-routes/GET /v1/events` alone would have to fold three deltas together and add the resolved inputs. The second node's latest bag came back the same way with its own resolved values. Six checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
