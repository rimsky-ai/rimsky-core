---
assessment: claim-producer-observability--admin-views
subject: story:claim-producer-observability
way: admin-views
release: d977250c
outcome: held
warrant: experiment:claim-producer-observability
---
# Rendering the custom views a producer declares

Both admin views the producer declares rendered, each returning a column schema and a render hint, and the parameterised one listed the seeded items for its required parameter. A view name the producer had not declared was refused rather than fabricated, so a dashboard cannot silently show an empty table for something that does not exist. The discovery half was checked on the same run: `catalog:http-routes/GET /v1/observability/claim-producers/{name}` reported the producer reachable and carried the same three capability flags and both view declarations. A dashboard therefore learns what to render from the control API and reads the data from the producer — the "without writing a custom backplane" the story asks for.

## Unverified remainder

None: the passing run demonstrates the way as promised across both views the producer declares.
