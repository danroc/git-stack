# UI Format: Space Separator + Red Behind Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Change the ahead/behind display from `(+3/-2)` to `(+3 -2)` with the behind count in red instead of yellow.

**Architecture:** Add `red` ANSI constant, change `behind` palette color to red, replace "/" separator with space in `formatEntry`.

**Tech Stack:** Go, testing via `go test`

---

## Files Changed

| File | Change |
|------|--------|
| `pkg/ui/color.go` | Add `red` constant, change `behind` from `yellow` to `red` |
| `pkg/ui/ui.go` | Replace "/" separator with space in `formatEntry` |
| `pkg/ui/render_test.go` | Update test expectation from `(+3/-2)` to `(+3 -2)` |

---

### Task 1: Add red color and update UI format

**Files:**
- Modify: `pkg/ui/color.go`
- Modify: `pkg/ui/ui.go:88-96`
- Modify: `pkg/ui/render_test.go:18`

- [ ] **Step 1: Add `red` constant and update `behind` color in color.go**

In `pkg/ui/color.go`, add `red` constant after `yellow`:

```go
const (
    reset  = "\033[0m"
    bold   = "\033[1m"
    dim    = "\033[2m"
    green  = "\033[32m"
    yellow = "\033[33m"
    red    = "\033[31m"   // NEW
)
```

Change `behind` in `colorPalette()`:

```go
// Before:
behind:    yellow,

// After:
behind:    red,
```

- [ ] **Step 2: Replace "/" separator with space in formatEntry**

In `pkg/ui/ui.go:88-96`, change:

```go
// Before:
if e.AheadCount > 0 || e.BehindCount > 0 {
    var parts []string
    if e.AheadCount > 0 {
        parts = append(parts, fmt.Sprintf("%s+%d%s", p.ahead, e.AheadCount, p.reset))
    }
    if e.BehindCount > 0 {
        parts = append(parts, fmt.Sprintf("%s-%d%s", p.behind, e.BehindCount, p.reset))
    }
    s += fmt.Sprintf(" (%s)", strings.Join(parts, "/"))
}

// After:
if e.AheadCount > 0 || e.BehindCount > 0 {
    s += "("
    if e.AheadCount > 0 {
        s += fmt.Sprintf("%s+%d%s", p.ahead, e.AheadCount, p.reset)
    }
    if e.BehindCount > 0 {
        if e.AheadCount > 0 {
            s += " "
        }
        s += fmt.Sprintf("%s-%d%s", p.behind, e.BehindCount, p.reset)
    }
    s += ")"
}
```

- [ ] **Step 3: Update test expectation in render_test.go**

In `pkg/ui/render_test.go:18`, change:

```go
// Before:
{"ahead and behind", &TreeEntry{BranchName: "feat-1", AheadCount: 3, BehindCount: 2}, "feat-1 (+3/-2)"},

// After:
{"ahead and behind", &TreeEntry{BranchName: "feat-1", AheadCount: 3, BehindCount: 2}, "feat-1 (+3 -2)"},
```

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/ui/ -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: PASS

---

## Self-Review

**1. Spec coverage:**
- Separator changed from "/" to space ✓
- Behind count color changed from yellow to red ✓
- Test updated to match new format ✓

**2. Placeholder scan:**
- No placeholders found. All code is concrete.

**3. Type consistency:**
- `red` constant uses `\033[31m` (standard ANSI red) ✓
- `behind` palette field already exists, just changing its value ✓

**4. Edge cases:**
- Only ahead: `(+3)` — no trailing space ✓
- Only behind: `(-2)` — no leading space ✓
- Both: `(+3 -2)` — space between, not after ✓
