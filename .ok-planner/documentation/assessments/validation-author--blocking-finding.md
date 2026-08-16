---
assessment: validation-author--blocking-finding
subject: story:validation-author
way: blocking-finding
release: d977250c
outcome: held
warrant: experiment:validation-author
---
# The service's own error finding refuses the registration

With the service declared as a peer that also serves validation, the audit registered a template the service's validator objects to. Registration was refused, and the refusal came back carrying the service's own finding — its class and the path the finding names, which is the role context the service was handed. Removing the validation mix-in from the peer's declared protocols let the same template register, so the refusal was the service author's validator and not one of the product's built-in checks. The author therefore extends validation beyond the built-in shape checks without changing anything in the product.

## Unverified remainder

One blocking finding from one service was exercised. The demonstration does not establish how findings from two validating peers on the same template are combined.
