---
audit: template-lifecycle
artifact: story:template-lifecycle
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:38:45Z
---

# An operator curates the catalog of workflows the stack offers

Supported. All five ways the story names — register a definition, mark it ready
to run, create live instances of it, retire it, remove it — were driven through
the public surface against a container of the released all-in-one image.
Registering returned a content-addressed id and the catalog listed the
definition as registered, with its stored spec readable back. Instance creation
was refused before the definition was marked ready and accepted after. While an
instance was live, retiring and removing were both refused; once the instance
was killed, retiring took effect and further instance creation was refused;
removal was refused while an instance record still referenced the definition
and succeeded once that record was gone, after which the catalog no longer
listed it. That last refusal arrives as a raw storage-constraint error rather
than a conflict naming the referencing record — the operation is correctly
refused, only its diagnosis is coarse. The body states a role, a capability set
and a mandatory benefit, names no surface and no mechanism, and carries no
history or forward-looking text.
