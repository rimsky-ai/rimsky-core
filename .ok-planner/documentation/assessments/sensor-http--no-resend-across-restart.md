---
assessment: sensor-http--no-resend-across-restart
subject: story:sensor-http
way: no-resend-across-restart
release: d977250c
outcome: held
warrant: experiment:sensor-http
---
# An unchanged body stays quiet, even after the sensor restarts

The audit showed the absence of a re-send positively rather than by waiting. With the watched document left alone, a second instance was created and its own first message is what proves the sensor kept polling; across that the first instance stayed at exactly two messages. The observation then repeats after the sensor process was restarted: a third instance sent its first message while the first instance still held exactly two. So the last-seen body survives the restart, and an operator restarting the sensor does not get the whole watch replayed into the graph.

## Unverified remainder

The demonstration covers one restart with the state store kept across it. It does not establish what a sensor started against an empty state store does with an unchanged upstream — the first poll after a state reset was not exercised.
