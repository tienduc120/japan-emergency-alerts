---
name: handling-errors
description: Use when a function can fail, when adding an error, or when reviewing how an error crosses a package boundary.
---

# Handle errors

Failure is a returned value, and each package owns the errors it raises.

## Rules

1. **Return `(T, error)`.** Never panic for a failure a caller could expect, and never invent a `Result[T]`.
2. **Wrap with `%w` and say what failed**: `fmt.Errorf("poll jma feed: %w", err)`. The message names the operation, not "error" or the package.
3. **Keep the cause when you name an outcome**: `fmt.Errorf("%w: %w", ErrBadMessage, err)`. `errors.Is` then finds both.
4. **Compare with `errors.Is` and `errors.As`** (`errors.AsType` for a type), never `==` or a type assertion.
5. **A package declares the sentinels its callers branch on**: `ErrBadMessage`, `ErrUnauthorized`. Name them `ErrSomething`, and a type `SomethingError` (`errname` checks).
6. **Translate once, at the boundary.** A transport error stays inside the source package; callers see the outcome they can act on.
7. **Never ignore one.** `_ = f()` needs a reason in a comment, and a deferred `Close` on a response body is the only usual case.
8. **A long-running loop survives a bad input.** A malformed message or a failed webhook post is logged and skipped; only a failure that makes the program unable to work returns from the loop. Never log a webhook URL or a secret in an error.

## Adding an outcome

1. Write the test first: the dependency fails in this way, expect this outcome.
2. Declare the sentinel next to the code that raises it, with a comment saying when it happens.
3. Return it, wrapped with the cause.
4. Handle it where the decision is made, and log the rest.

## Before you finish

Walk one failing path from the place it starts to where it is logged or handled, and check that each package boundary is a translation you can point to.
