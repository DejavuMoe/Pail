# Usage examples

## Audit

```text
$prototype-first-ui audit
Record this product's current screens, actions, states, and interfaces in docs/ui/capabilities.md.
```

## Explore

```text
$prototype-first-ui explore
Explore three navigation and workspace layouts for <product>, using the existing capabilities and design system.
```

## Prototype

```text
$prototype-first-ui prototype
Prototype <feature> in designs/<project>/. Include the normal flow, empty state, errors, and cancellation, then present it for review.
```

## Approve

```text
$prototype-first-ui approve
I approve the current designs/<project>/index.html. Record its approval and create the design-only commit.
```

## Implement

```text
$prototype-first-ui implement
Implement <feature> from the approved designs/<project>/index.html at design commit <SHA>.
```

## Sync

```text
$prototype-first-ui sync
Compare the production <surface> with its approved prototype and fix implementation drift.
```

## Verify

```text
$prototype-first-ui verify
Check <feature> against its approved prototype, including interactions, content, responsive layout, and keyboard accessibility.
```

## Bootstrap

```text
$prototype-first-ui bootstrap
Clean this repository and replace its local Git history on branch <branch> after creating and verifying an external recovery snapshot. Preserve the application and leave the remote unchanged.
```
