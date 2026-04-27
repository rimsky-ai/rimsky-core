# Cold Read Style Guide

This guide defines how to write **context-complete**, **cold-readable** code for AI agent-assisted development. Follow these rules when writing new code or modifying existing code.

For the philosophy behind these rules, see the [Cold Read Manifesto](./cold-read-manifesto.md).

---

## File Organization

### One Feature Per File

Each distinct feature, endpoint, or operation gets its own file.

**Web services:**
```
features/
  orders/
    create.py          # POST /orders
    get.py             # GET /orders/:id
    list.py            # GET /orders
    cancel.py          # POST /orders/:id/cancel
    types.py           # shared types for orders feature
  users/
    register.py        # POST /users
    login.py           # POST /auth/login
    profile.py         # GET /users/:id
```

**Web applications:**
```
pages/
  home.tsx
  dashboard.tsx
  settings.tsx
  orders/
    list.tsx
    detail.tsx
    create.tsx
```

**CLI tools:**
```
commands/
  init.py
  build.py
  deploy.py
  config.py
```

### Flat Directory Structure

Limit nesting to two levels maximum. Avoid structures like `src/modules/features/orders/services/create/index.ts`.

**Acceptable:**
```
features/orders/create.py
shared/database.py
```

**Not acceptable:**
```
src/modules/features/orders/services/create/handler.py
```

### Co-locate Related Files

Keep tests, types, and helpers next to the code they support.

```
features/orders/
  create.py
  create_test.py
  types.py           # types used by multiple files in orders/
```

Do not separate tests into a parallel directory tree.

### Feature Index

Maintain a `feature-index.md` file at the repository root that maps features to their primary files and dependencies. This supports navigation at scale and helps agents quickly locate relevant code.

```markdown
| Feature | Primary files | Shared deps | Notes |
| --- | --- | --- | --- |
| Orders - Create | `features/orders/create.py` | `shared/database.py`, `shared/auth.py` | Emits `order.created` |
| Orders - Cancel | `features/orders/cancel.py` | `shared/database.py`, `shared/auth.py` | Requires order ownership |
```

Update the index when:
- Adding a new feature
- Changing a feature's primary files or dependencies
- Adding critical invariants or ownership notes

See the [Process](#process) section for maintenance guidelines.

---

## Code Structure

### File Length Guideline: ~500 Lines

If a file significantly exceeds 500 lines, consider splitting by sub-feature. This is a guideline, not a hard rule—semantic cohesion matters more than line counts.

```
# Before (too long)
features/orders/create.py  # 700 lines

# After (split by concern)
features/orders/create.py              # orchestration, ~100 lines
features/orders/create_validation.py   # input validation, ~200 lines
features/orders/create_persistence.py  # database operations, ~200 lines
features/orders/create_notifications.py # emails/events, ~200 lines
```

Do **not** split by abstraction layer (service/repository/controller).

### Function Length Guideline: ~100 Lines

Functions should generally not exceed 100 lines. Extract helper functions into the same file when needed. Again, semantic cohesion trumps arbitrary limits—a 120-line function that does one clear thing is better than artificial splits.

### Maximum Nesting Depth: 3 Levels

Avoid deeply nested control flow. Use early returns to flatten logic.

**Avoid:**
```python
def process(data):
    if data:
        if data.valid:
            if data.user:
                if data.user.active:
                    # actual logic here
```

**Prefer:**
```python
def process(data):
    if not data:
        return Error("no data")
    if not data.valid:
        return Error("invalid data")
    if not data.user:
        return Error("no user")
    if not data.user.active:
        return Error("inactive user")

    # actual logic here
```

---

## Dependencies and Imports

### Maximum Import Depth: 2 Files

To understand any feature file, you should need to read at most 2 additional files.

**Acceptable dependency chain:**
```
features/orders/create.py
  → shared/database.py
  → shared/types.py
```

**Not acceptable:**
```
features/orders/create.py
  → services/order_service.py
    → services/base_service.py
      → core/executor.py
```

### Allowed Shared Dependencies

Import from `shared/` only for:

- Database connections and queries
- HTTP client utilities
- Logging
- Authentication/authorization checks
- Configuration access
- Common type definitions

### Blessed Invariants

Some cross-cutting concerns require consistent behavior across all features. These "blessed invariants" are stable shared modules with stricter requirements:

**Designate a module as a blessed invariant when:**
- It enforces security or correctness properties (auth, validation)
- Inconsistent implementations would cause system-wide problems
- It is stable and rarely modified

**Requirements for blessed invariants:**
1. Must have an `@agent-contract` block
2. Must have comprehensive tests
3. Changes require explicit review and approval
4. Must be documented in the feature index under "Shared deps"

**Example blessed invariants:**

```
shared/
  auth.py           # @blessed-invariant: authentication/authorization
  error_types.py    # @blessed-invariant: standard error shapes
  telemetry.py      # @blessed-invariant: logging and metrics
  database.py       # @blessed-invariant: connection management
```

**The @blessed-invariant annotation:**

```python
# @blessed-invariant: authentication
# @agent-contract
# - Verifies JWT tokens and extracts user context
# - Use as: user = auth.require_user(request)
# - Raises AuthError if token invalid or expired
# - Does NOT handle authorization (use auth.require_permission)
# - Thread-safe: yes
#
# STABILITY: Changes to this module affect all authenticated endpoints.
# Modifications require security review.

def require_user(request: Request) -> User:
    ...
```

Blessed invariants are the exception to Cold Read principles—they exist because consistency matters more than isolation for these specific concerns.

### Forbidden Patterns

Do not import:
- Base classes that require reading to understand behavior
- Service classes that wrap other service classes
- Abstract factories or dependency injection containers
- Anything with "Base", "Abstract", or "Manager" in the name (unless you wrote it and it's under 50 lines)

---

## Duplication Tracking

### The @source Annotation

When duplicating code from another location, annotate the copy with its source:

```python
# @source: features/orders/create.py:validate_order_items
def validate_order_items(items):
    if not items:
        raise ValidationError("items required")
    if len(items) > 100:
        raise ValidationError("too many items")
```

**Format:** `@source: <file_path>:<function_or_section_name>`

### The Canonical Source

The first implementation of a pattern is the "canonical source." It does not need a `@source` annotation—it is the source. All copies reference it.

```python
# features/orders/create.py (canonical - no annotation needed)
def validate_order_items(items):
    if not items:
        raise ValidationError("items required")
    if len(items) > 100:
        raise ValidationError("too many items")
```

```python
# features/orders/update.py (copy - annotated)
# @source: features/orders/create.py:validate_order_items
def validate_order_items(items):
    if not items:
        raise ValidationError("items required")
    if len(items) > 100:
        raise ValidationError("too many items")
```

### Documenting Divergence

When a copy intentionally differs from its source, mark it as diverged and explain why:

```python
# @source: features/orders/create.py:validate_order_items
# @diverged: true
# @reason: Updates allow empty items (to clear the order)
def validate_order_items_for_update(items):
    # Note: items can be None or empty for updates
    if items is not None and len(items) > 100:
        raise ValidationError("too many items")
```

This makes intentional differences visible and prevents accidental "fixes" that reintroduce removed behavior.

### Optional Annotations

Add context when helpful:

```python
# @source: features/orders/create.py:validate_order_items
# @note: Keep in sync - both enforce same business rules
# @last-synced: 2025-01-15
def validate_order_items(items):
    ...
```

### Propagating Changes

When you modify canonical source code:

1. Search for `@source: <file>:<function>` to find all copies
2. For each copy:
   - If `@diverged: true`, evaluate whether the change applies
   - If not diverged, apply the same change
   - If the change doesn't apply, add `@diverged: true` with reason
3. Update `@last-synced` if using that annotation

---

## Agent Contracts for Shared Code

### The @agent-contract Annotation

Shared infrastructure code should include a contract block that agents can read without understanding the implementation:

```python
# @agent-contract
# - Provides PostgreSQL database connection pooling
# - Use as: with db.connection() as conn: ...
# - Use as: with db.transaction() as tx: ...
# - Transactions auto-rollback on unhandled exception
# - Does NOT handle retries (caller must implement)
# - Thread-safe: yes
# - Async: no, use async_database.py for async contexts
# - Max connections: configured via DATABASE_POOL_SIZE env var
class Database:
    ...
```

### Contract Format

Contracts should answer:
- **What does this provide?** (one-line summary)
- **How do I use it?** (basic usage patterns)
- **What does it handle?** (automatic behaviors)
- **What doesn't it handle?** (caller responsibilities)
- **Is it thread-safe / async?**
- **Configuration?** (if applicable)

### When to Write Contracts

Write `@agent-contract` blocks for:
- Database clients
- HTTP clients
- Cache clients
- Authentication utilities
- Logging utilities
- Configuration loaders
- Any shared code imported by 5+ features

---

## Abstraction Rules

### When to Extract Shared Code

Extract code into a shared module only when ALL conditions are met:

1. **Three or more** call sites exist
2. The logic is **identical** across all call sites (not just similar)
3. The shared code is **stable**—unlikely to need per-caller modifications
4. The shared code is **small**—under 50 lines

If extraction is warranted, add an `@agent-contract` block.

### When to Keep Code Duplicated (with Tracking)

Keep code duplicated when:

- Only 2 call sites exist
- Logic is similar but has different edge cases
- You might need to modify one instance without affecting others
- The "shared" version would need parameters to handle differences

**Always use `@source` annotations when duplicating.**

**Example of acceptable tracked duplication:**

```python
# features/orders/create.py (canonical source)
def validate_order_input(data):
    if not data.customer_id:
        raise ValidationError("customer_id required")
    if not data.items:
        raise ValidationError("items required")
    if len(data.items) > 100:
        raise ValidationError("too many items")

# features/orders/update.py
# @source: features/orders/create.py:validate_order_input
# @diverged: true
# @reason: items not required for update, only validated if present
def validate_order_update(data):
    if not data.customer_id:
        raise ValidationError("customer_id required")
    if data.items is not None and len(data.items) > 100:
        raise ValidationError("too many items")
```

### Inline Over Call

Prefer inlining short operations over calling utility functions.

**Prefer:**
```python
user_ids = [u.id for u in users if u.active]
```

**Over:**
```python
user_ids = extract_ids(users, filter_fn=lambda u: u.active)
```

Only extract utilities when they provide significant value (complexity hiding, 3+ uses, error-prone operations).

---

## Explicit Code

### No Decorators for Behavior

Do not use decorators that modify function behavior in non-obvious ways.

**Avoid:**
```python
@retry(attempts=3)
@cached(ttl=300)
@transactional
def create_order(data):
    ...
```

**Prefer:**
```python
def create_order(db, cache, data):
    cached_result = cache.get(f"order:{data.id}")
    if cached_result:
        return cached_result

    with db.transaction():
        for attempt in range(3):
            try:
                result = _do_create_order(db, data)
                cache.set(f"order:{data.id}", result, ttl=300)
                return result
            except RetryableError:
                if attempt == 2:
                    raise
                continue
```

The explicit version is longer but requires no external context to understand.

### No Implicit Dependency Injection

Pass dependencies as explicit parameters.

**Avoid:**
```python
class OrderService:
    @inject
    def __init__(self, db: Database, cache: Cache):
        self.db = db
        self.cache = cache
```

**Prefer:**
```python
def create_order(db: Database, cache: Cache, data: OrderInput) -> Order:
    ...

# At call site:
order = create_order(db=get_database(), cache=get_cache(), data=input)
```

### No Convention-Based Magic

Make routing, configuration, and behavior explicit.

**Avoid:**
```python
# File at api/orders/create.py automatically maps to POST /api/orders/create
```

**Prefer:**
```python
# routes.py
router.post("/api/orders", handlers.orders.create)
router.get("/api/orders/:id", handlers.orders.get)
```

### Configuration as Visible Values

Use explicit configuration objects, not environment lookups scattered through code.

**Avoid:**
```python
def create_order(data):
    timeout = int(os.environ.get("ORDER_TIMEOUT", "30"))
    retries = int(os.environ.get("ORDER_RETRIES", "3"))
```

**Prefer:**
```python
@dataclass
class OrderConfig:
    timeout: int = 30
    retries: int = 3

def create_order(config: OrderConfig, data: OrderInput):
    # config.timeout and config.retries are explicit
```

---

## Type Definitions

### Required Types at Boundaries

Define explicit types for:

- API request inputs
- API response outputs
- Database models
- External service interfaces
- Configuration objects
- Interfaces between features

```python
# features/orders/types.py
@dataclass
class CreateOrderInput:
    customer_id: str
    items: list[OrderItem]
    shipping_address: Address

@dataclass
class CreateOrderOutput:
    order_id: str
    status: OrderStatus
    estimated_delivery: datetime

@dataclass
class OrderItem:
    product_id: str
    quantity: int
    unit_price: Decimal
```

### Internal Flexibility

Inside a feature's implementation, typing every variable is not required. Use types at interfaces, be pragmatic internally.

---

## Error Handling

### Explicit Error Returns

Prefer returning error types over throwing exceptions for expected failure cases.

**Prefer:**
```python
def create_order(data) -> Order | ValidationError | DatabaseError:
    if not data.valid:
        return ValidationError("invalid input")
    ...
```

Or use result types:

```python
def create_order(data) -> Result[Order, OrderError]:
    ...
```

### Catch Specific Exceptions

Never catch bare `Exception`. Catch specific error types.

**Avoid:**
```python
try:
    result = risky_operation()
except Exception:
    return default_value
```

**Prefer:**
```python
try:
    result = risky_operation()
except ConnectionError:
    return default_value
except ValidationError:
    raise  # re-raise, don't swallow
```

---

## Testing

### Test File Location

Test files live next to the code they test.

```
features/orders/create.py
features/orders/create_test.py
```

### Test Independence

Each test file should be runnable independently. Do not create shared test fixtures in separate files that must be understood to read the tests.

### Test Naming

Name tests to describe the scenario, not the implementation.

```python
def test_create_order_succeeds_with_valid_input():
    ...

def test_create_order_fails_when_customer_not_found():
    ...

def test_create_order_fails_when_inventory_insufficient():
    ...
```

---

## Cross-Feature Changes

When a change affects multiple features:

### For Tracked Duplicates

1. Modify the canonical source
2. Run: `grep -r "@source: <file>:<function>" .`
3. For each result:
   - Check if `@diverged: true`
   - If not diverged, apply the same change
   - If diverged, evaluate whether change applies
4. Commit with a message noting all affected files

### For New Patterns

When introducing a pattern that will be duplicated:

1. Implement in the first location (this becomes canonical)
2. When copying to second location, add `@source` annotation
3. Consider whether extraction is warranted (usually wait for 3+ uses)

### For Large-Scale Changes

If a change affects 10+ files:

1. Consider temporary extraction into shared module
2. Update all call sites to use shared version
3. Make the change once
4. Optionally re-inline with `@source` annotations

---

## Process

### Duplication Audit Workflow

When modifying code that may have duplicates:

**1. Before modifying canonical source:**
```bash
# Find all copies of this function
grep -r "@source: features/orders/create.py:validate_order_items" .
```

**2. Evaluate each copy:**
- If `@diverged: true` → read the `@reason` and decide if change applies
- If not diverged → plan to apply the same change
- If change shouldn't apply → add `@diverged: true` with reason

**3. Document in commit/PR:**
```
Modified validate_order_items in features/orders/create.py

Propagated to:
- features/orders/update.py (applied same change)
- features/orders/bulk_create.py (marked diverged: bulk has different limits)
```

**4. Update feature index** if propagation changed dependencies.

### Feature Index Maintenance

Maintain `feature-index.md` at the repository root (see template). Update it when:

- A new feature is added
- A feature's primary files change
- Shared dependencies are added or removed
- Ownership or critical invariants change

**Review the index** as part of the PR checklist for any feature-level changes.

---

## Enforcement

### Recommended Static Analysis

Configure your linter/CI to check:

- Import depth (warn if > 2 levels)
- Cross-feature imports (features should only import from shared/ or their own directory)
- `@source` annotation validity (referenced files/functions exist)
- File length (warn above 500 lines)

### Example Pre-commit Hook

```bash
#!/bin/bash
# Validate @source annotations point to real code

grep -r "@source:" --include="*.py" . | while read line; do
    file=$(echo "$line" | cut -d: -f1)
    source_ref=$(echo "$line" | grep -o "@source: [^#]*" | sed 's/@source: //')
    source_file=$(echo "$source_ref" | cut -d: -f1)
    source_func=$(echo "$source_ref" | cut -d: -f2)

    if [ ! -f "$source_file" ]; then
        echo "ERROR: $file references non-existent source $source_file"
        exit 1
    fi

    if ! grep -q "def $source_func\|class $source_func" "$source_file"; then
        echo "WARNING: $file references $source_func not found in $source_file"
    fi
done
```

---

## PR Review Checklist

Use this checklist when reviewing pull requests. Both authors and reviewers should verify these items.

### Code Structure

- [ ] Each feature is in its own file
- [ ] File is around 500 lines or less (or has good reason to be longer)
- [ ] Functions are around 100 lines or less
- [ ] Nesting depth is 3 or less
- [ ] Import depth is 2 files or less
- [ ] No behavior-modifying decorators
- [ ] Dependencies are explicit parameters
- [ ] Types defined for all boundaries

### Duplication Tracking

- [ ] Duplicated code has `@source` annotations pointing to canonical source
- [ ] Diverged duplicates have `@diverged: true` with `@reason`
- [ ] If canonical source was modified, copies were audited and updated/diverged
- [ ] Audit results noted in PR description

### Shared Code

- [ ] Shared infrastructure has `@agent-contract` blocks
- [ ] Blessed invariants have `@blessed-invariant` annotation
- [ ] Changes to blessed invariants have appropriate review/approval

### Testing & Co-location

- [ ] Tests are co-located with code (same directory)
- [ ] Test files are independently runnable

### Process

- [ ] Feature index updated if features/dependencies changed
- [ ] Commit messages describe intent, not just "what"

---

## Quick Reference

| Do | Don't |
|---|---|
| One feature per file | Group by technical layer |
| Flat directories (2 levels max) | Deep nesting |
| Explicit parameters | Dependency injection |
| Inline short operations | Extract tiny utilities |
| Duplicate with `@source` tracking | Untracked duplication |
| Duplicate with `@source` tracking | Force shared abstractions too early |
| `@agent-contract` on shared code | Undocumented shared utilities |
| Types at boundaries | Types on every variable |
| Co-locate tests | Separate test directory tree |
| Early returns | Deep nesting |
| Specific error catching | Bare `except` |
| Visible configuration | Scattered env lookups |

---

## Annotation Reference

### @source
Marks code as duplicated from another location.
```python
# @source: path/to/file.py:function_name
```

### @diverged
Indicates intentional differences from source.
```python
# @source: path/to/file.py:function_name
# @diverged: true
# @reason: Explanation of why this differs
```

### @note
Additional context about the duplication.
```python
# @source: path/to/file.py:function_name
# @note: Keep in sync for billing consistency
```

### @last-synced
Optional tracking of when copy was last verified against source.
```python
# @source: path/to/file.py:function_name
# @last-synced: 2025-01-15
```

### @agent-contract
Documents shared code for agent consumption without reading implementation.
```python
# @agent-contract
# - What it provides
# - How to use it
# - What it handles automatically
# - What caller must handle
# - Thread-safety and async status
```

### @blessed-invariant
Marks a shared module as a stable, cross-cutting concern requiring stricter review.
```python
# @blessed-invariant: category (e.g., authentication, error-handling, telemetry)
# @agent-contract
# - Contract details...
#
# STABILITY: Note about change impact and review requirements.
```
