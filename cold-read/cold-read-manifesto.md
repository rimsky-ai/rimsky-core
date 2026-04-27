# The Cold Read Manifesto

## Context-Complete Code for AI Agents

This document explains why codebases optimized for AI coding agents require different design principles than traditional human-maintained code.

---

## What is Cold Read?

**Cold Read** is a methodology for writing code that AI coding agents can understand without prior context—like an actor reading a script "cold" without preparation.

It implements the broader principle of **Context-Complete** code: every file contains sufficient context to be understood and safely modified without reconstructing knowledge from elsewhere in the codebase.

The name comes from theater: a "cold read" is when an actor performs a script they've never seen before. AI agents perform cold reads on every task—they have no persistent memory of your codebase between sessions.

---

## The Core Problem

Traditional software engineering wisdom evolved around a key assumption: **developers learn a codebase once and retain that knowledge across many tasks**. This assumption justifies practices like:

- Deep abstraction hierarchies (learn it once, benefit forever)
- DRY principles (change one place, affect everywhere)
- Implicit behavior through conventions (Rails magic, dependency injection)
- Separation by technical layer (controllers/, services/, models/)

AI coding agents violate this assumption. An agent:

- **Reads code fresh each session** with no persistent memory of past understanding
- **Pays context costs per-task**, not amortized across months of work
- **Cannot "just know"** that your team's `BaseService` class handles retries
- **Has finite context windows** that fill up chasing import chains

When an agent needs to modify a feature, it must load that feature's code *and* every abstraction it depends on *and* understand how those abstractions behave in this specific context. A three-line function call might require 500 lines of context to understand.

**The cost model is inverted.** Humans pay upfront learning costs for long-term efficiency. Agents pay understanding costs on every single task.

---

## Design for the Present, Enable the Future

This manifesto optimizes for current agent capabilities. We make no claim that these principles are eternal.

**The key insight**: Code that is maximally isolated and explicit can always be refactored toward more abstraction. The reverse is harder—extracting tangled abstractions into isolated features requires understanding the abstractions first.

Future agents with larger context windows and persistent memory should, by definition, be able to:

1. Identify duplication patterns across a Cold Read codebase
2. Evaluate whether consolidation is safe
3. Refactor toward the optimal abstraction level for their capabilities

**Each generation of agents can find its own optimal balance.** A codebase following this guide gives future agents a clean starting point. A codebase with deep abstractions may trap current agents in a maze they cannot navigate.

We optimize for the present while preserving optionality for the future.

---

## The Three Goals

Agent-optimized code should:

1. **Minimize context required to understand a feature.** An agent should be able to read one file (or a small, obvious set of files) and know everything needed to safely modify that feature.

2. **Isolate blast radius of changes.** Modifying feature A should not risk breaking features B, C, and D through shared abstractions. An agent working on one endpoint should not need to reason about all other endpoints.

3. **Enable review without archaeology.** A human or agent reviewing a change should understand what the code does by reading the code itself, not by reconstructing a mental model of the abstraction stack.

---

## The Principles

### 1. Locality of Behavior

> This principle, coined by Carson Gross (creator of HTMX), is foundational to Cold Read. We adopt it directly.

Code should be understandable where it is written, not where it is called from or where its abstractions are defined.

**Traditional approach:**
```python
# orders/service.py
class OrderService(BaseService):
    def create(self, data):
        return self.execute(CreateOrderCommand(data))
```
To understand this, you must read `BaseService`, `execute`, `CreateOrderCommand`, and whatever middleware or decorators are involved.

**Cold Read approach:**
```python
# orders/create.py
def create_order(db: Database, data: OrderInput) -> Order:
    validate_order_input(data)
    order = Order(id=generate_id(), **data)
    db.orders.insert(order)
    emit_event("order.created", order)
    return order
```
The behavior is visible in the function. Dependencies are explicit parameters.

### 2. Isolation by Feature, Not Layer

Group code by what it does, not by what technical role it plays.

**Traditional structure:**
```
controllers/
  orders_controller.py
  users_controller.py
services/
  order_service.py
  user_service.py
models/
  order.py
  user.py
```
Understanding "create order" requires reading three files in three directories, plus whatever base classes they inherit from.

**Feature-isolated structure:**
```
features/
  orders/
    create.py      # everything for creating an order
    list.py        # everything for listing orders
    cancel.py      # everything for canceling an order
  users/
    register.py
    login.py
```
Each file is self-contained. An agent working on order creation reads one file.

### 3. Explicit Over Implicit

Visible code is understandable code. Magic is context an agent doesn't have.

**Avoid:**
- Decorators that transform behavior (`@retry`, `@cached`, `@transactional`)
- Dependency injection containers
- Metaprogramming and reflection
- Convention-based routing ("this file is at `/api/orders` because of its path")
- Implicit middleware chains

**Prefer:**
- Explicit function calls (`result = with_retry(3, lambda: fetch_data())`)
- Parameters passed directly
- Explicit route registration
- Configuration objects you can read

### 4. Tracked Duplication Over Hidden Coupling

The traditional DRY principle optimizes for changing shared behavior. Agent-optimized code prioritizes safe, isolated changes—but acknowledges that duplication has costs that must be managed.

**The duplication tradeoff:**
- Duplication isolates features from each other
- But untracked duplication leads to drift and inconsistency
- Therefore: duplicate intentionally and track explicitly

**Extract shared code only when:**
- Three or more call sites exist AND
- The logic is genuinely identical (not just similar) AND
- The shared code is stable and unlikely to need per-caller modification

**Keep code duplicated when:**
- Two call sites (not enough to justify coupling)
- Logic is similar but has diverging edge cases
- You might need to modify one case without affecting others

**Always track duplication** using the `@source` annotation (see Style Guide). This eliminates the "search and modify" problem—when source code changes, agents know exactly where to propagate updates.

### 5. Shallow Dependencies

Allow one level of utility functions. Forbid abstractions of abstractions.

**Acceptable:**
```
feature/create_order.py
  → imports shared/database.py
  → imports shared/validation.py
```

**Not acceptable:**
```
feature/create_order.py
  → imports services/order_service.py
    → imports services/base_service.py
      → imports core/command_executor.py
        → imports core/middleware_chain.py
```

If understanding a feature requires reading more than 2-3 files, the abstraction is too deep.

### 6. Typed Boundaries, Flexible Interiors

Use explicit types and schemas at the edges:
- API request/response schemas
- Database models
- Interfaces between features
- External service contracts

Inside a feature, allow pragmatic flexibility. The goal is to make the contracts clear, not to type every internal variable.

### 7. Co-locate Everything Related

Tests, types, constants, and helpers for a feature live with that feature, not in a separate tree.

```
features/orders/create.py
features/orders/create_test.py
features/orders/types.py
```

Not:
```
src/features/orders/create.py
tests/features/orders/test_create.py
types/orders.py
```

An agent should find everything about a feature in one place.

### 8. Agent-Readable Contracts

When shared code exists (infrastructure, stable utilities), it should carry machine-readable documentation that agents can consume without reading the full implementation.

```python
# @agent-contract
# - Provides database connection and transaction management
# - Use as: with db.transaction(): ...
# - Automatically rolls back on unhandled exception
# - Does NOT handle retries (caller must implement)
# - Thread-safe: yes
# - Async: no, use async_database.py for async contexts
class Database:
    ...
```

This allows agents to use shared infrastructure confidently without loading hundreds of lines of implementation into context.

---

## Managing Duplication

Duplication is not free. Untracked duplication leads to:
- Inconsistent behavior when one copy is updated but others aren't
- Difficulty knowing whether differences are intentional or accidental
- Confusion during review about what code is "supposed to" match

### The Source Tracking Pattern

Every piece of intentionally duplicated code should reference its canonical source:

```python
# @source: features/orders/create.py:validate_order_items
# @note: Identical logic; update both if business rules change
def validate_order_items(items):
    if not items:
        raise ValidationError("items required")
    if len(items) > 100:
        raise ValidationError("too many items")
```

When the source changes, an agent can:
1. Find all files with `@source: features/orders/create.py:validate_order_items`
2. Evaluate whether each copy should receive the same change
3. Update or explicitly diverge with documentation

### Intentional Divergence

When duplicated code intentionally differs, document why:

```python
# @source: features/orders/create.py:validate_order_items
# @diverged: true
# @reason: Updates allow empty items array (clears the order)
def validate_order_items_for_update(items):
    if items is not None and len(items) > 100:
        raise ValidationError("too many items")
```

This makes drift visible and intentional rather than accidental.

---

## Cross-Feature Changes

When changes must propagate across features (new auth scheme, logging format, etc.):

### Option 1: Tracked Propagation

If code uses `@source` annotations, the agent:
1. Modifies the canonical source
2. Queries for all `@source` references to that location
3. Updates each copy, evaluating whether divergence is needed
4. Documents any new divergences

### Option 2: Temporary Extraction

For large-scale changes:
1. Extract the pattern into a shared module temporarily
2. Update all call sites to use the shared version
3. Make the change once
4. Optionally re-inline if isolation is preferred

### Option 3: Feature Flags

For behavioral changes that should roll out gradually:
1. Implement new behavior behind a flag
2. Update features one at a time
3. Remove flag when complete

All three approaches are explicit and auditable, unlike hoping that changing a base class doesn't break anything.

---

## Enforcement and Verification

Principles without enforcement become suggestions. Consider:

### Static Analysis

- **Import depth checking**: Flag files that import chains deeper than 2 levels
- **Cross-feature dependency detection**: Warn when feature A imports from feature B (should go through shared/ or types/)
- **Source annotation validation**: Verify `@source` references point to real code

### Architectural Tests

```python
def test_features_are_isolated():
    for feature_dir in glob("features/*"):
        imports = extract_imports(feature_dir)
        for imp in imports:
            assert imp.startswith("shared/") or imp.startswith("features/" + feature_dir.name)
```

### Pre-commit Hooks

- Verify new files are in the correct structure
- Check that `@source` annotations are valid
- Flag files exceeding size guidelines

---

## Addressing Objections

### "This leads to massive code duplication"

Yes. That's intentional. But with `@source` tracking, duplication becomes managed rather than chaotic. The question is: what's more expensive?

- Option A: An agent modifies shared code and breaks three other features
- Option B: An agent modifies tracked duplicated code and propagates changes explicitly

For agent-assisted development, Option B is safer. The `@source` pattern makes it tractable.

### "Files will get huge"

Set a guideline (around 500 lines). If a feature exceeds this, split by sub-feature, not by abstraction layer. `orders/create.py` becomes `orders/create_validation.py` and `orders/create_persistence.py`, not `orders/service.py` and `orders/repository.py`.

### "This is harder for humans to maintain"

Possibly. This is a tradeoff. If most changes are made by agents with human review, optimize for agents. If most changes are made by humans, traditional patterns may be better.

The hybrid approach: use Cold Read for feature code where agents work, traditional patterns for stable infrastructure code that rarely changes.

### "What about global changes?"

Use tracked propagation, temporary extraction, or feature flags as described above. All three are more work than modifying one abstraction, but all three are safer and more auditable.

### "Won't future agents handle abstraction better?"

Yes. And those agents can refactor this codebase toward more abstraction when they're capable of doing so safely. A Cold Read codebase is easy to consolidate. An abstraction-heavy codebase is hard to untangle.

---

## When to Apply These Principles

**Strong fit:**
- Web services with many independent endpoints
- CRUD applications
- Microservices
- Projects where agents do most implementation work

**Weaker fit:**
- Complex algorithmic code with inherent abstraction needs
- Libraries designed for human consumption
- Projects with heavy human involvement and light agent use

**Hybrid approach:**
- `shared/` or `infrastructure/`: Traditional patterns, well-documented, stable
- `features/`: Cold Read style—isolated, duplicated with tracking

---

## Summary

| Traditional Wisdom | Cold Read Principle |
|---|---|
| Don't Repeat Yourself | Duplicate and Track |
| Separate by technical layer | Isolate by feature |
| Abstract to hide complexity | Make behavior visible |
| Implicit conventions | Explicit configuration |
| Deep, reusable hierarchies | Shallow, disposable utilities |
| Assume developers know the codebase | Assume agents read fresh each time |

The goal is not to write "worse" code. The goal is to write **context-complete** code optimized for how AI agents actually work: reading cold each time, with limited context, trying to make safe changes to isolated features.

Code that an agent can cold read is code an agent can safely modify.

Code that tracks its own duplication is code that can evolve safely.

Code that is maximally explicit today can be refactored by more capable agents tomorrow.

---

## Glossary

| Term | Definition |
|------|------------|
| **Context-Complete** | Code containing all necessary context for understanding without external knowledge |
| **Cold Read** | The methodology for achieving context-complete code, optimized for AI agents |
| **Cold-readable** | Adjective describing code understandable without prior context |
| **Drift** | (n.) Unintentional divergence between code copies; (v.) the tooling that detects it |
| **Canonical source** | The authoritative version of duplicated code that copies reference |
| **Blessed invariant** | Shared code that must remain consistent across all features |

---

## References and Further Reading

The Cold Read methodology builds on ideas from several sources:

### Foundational Concepts

- **Locality of Behavior** — Carson Gross (HTMX creator) coined this principle, derived from Richard Gabriel's *Patterns of Software*. The idea that code behavior should be obvious where it is written, not scattered across files.
  - [Locality of Behavior (htmx.org)](https://htmx.org/essays/locality-of-behaviour/)
  - [Patterns of Software — Richard Gabriel](https://www.dreamsongs.com/Files/PatternsOfSoftware.pdf)

- **Local-First Software** — Ink & Switch's research on offline-first applications with CRDTs. While addressing a different problem (data ownership and sync), their work highlights how "local" thinking changes architectural assumptions. Cold Read applies similar "self-contained" thinking to code comprehension rather than data sync.
  - [Local-First Software — Ink & Switch](https://www.inkandswitch.com/essay/local-first/)

### Related Architectural Patterns

- **Self-Contained Systems (SCS)** — An architectural style where each system includes UI, logic, and data. Cold Read applies similar isolation principles at the feature/file level rather than the service level.
  - [Self-Contained Systems](https://scs-architecture.org/)

- **Vertical Slice Architecture** — Organizing code by feature rather than technical layer. Cold Read extends this with explicit duplication tracking.
  - [Vertical Slice Architecture — Jimmy Bogard](https://www.jimmybogard.com/vertical-slice-architecture/)

### AI Agent Context

- **Context Engineering** — The emerging discipline of treating LLM context as a first-class architectural concern. Cold Read is a code-level implementation of context engineering principles.
  - [Context Engineering — Google Developers Blog](https://developers.googleblog.com/en/architecting-efficient-context-aware-multi-agent-framework-for-production/)

### Counterpoint: Traditional Wisdom

Understanding what Cold Read argues against:

- **DRY (Don't Repeat Yourself)** — The Pragmatic Programmer's principle that Cold Read intentionally relaxes in favor of tracked duplication.
  - [The Pragmatic Programmer — Hunt & Thomas](https://pragprog.com/titles/tpp20/the-pragmatic-programmer-20th-anniversary-edition/)

- **Clean Architecture** — Robert Martin's layered architecture. Cold Read prefers feature isolation over layer separation for agent-modified code.
  - [Clean Architecture — Robert C. Martin](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)
