# Masir Desktop — Phase 1 (Foundations) Implementation Guide

> **For Go beginners & contributors**: This guide provides conceptual mental models, architectural options with trade-offs, Go idioms, and sharp edges for the first five foundation tasks (**W01–W05** / Issues #2–#6). 
> 
> *It does not force a single implementation path.* Instead, it outlines common Go patterns so you can choose the structure that best fits your style while respecting the project invariants in `AGENTS.md`.

---

## Quick Navigation

- [W01 — Page Router and Feature Files (#2)](#w01--page-router-and-feature-files-2)
- [W02 — One RPC Layer in the App (#3)](#w02--one-rpc-layer-in-the-app-3)
- [W03 — Additive Daemon API for the Screens (#4)](#w03--additive-daemon-api-for-the-screens-4)
- [W04 — Compact and Wide Layouts (#5)](#w04--compact-and-wide-layouts-5)
- [W05 — Shared Controls for the New Screens (#6)](#w05--shared-controls-for-the-new-screens-6)

---

## W01 — Page Router and Feature Files (#2)

### Background & Motivation
Currently, adding or modifying a screen requires editing an enum (`type screen int`), adding a branch to `switch a.lastSub` in `view_main.go`, updating `subHeight()`, modifying `openScreen()`, and adding code to already massive files (`app.go`, `view_main.go`, `view_pages.go`). The goal is to define a page contract and a lightweight router so future weeks (Subscriptions, Rule Editor, Logs, etc.) only require adding one file and one registry line.

### Key Go Concepts & Mental Model
1. **Single Package, Multiple Files (`package main`)**:
   - In Go, all `.go` files inside `apps/masir-gui/` belong to `package main`.
   - Any function, struct, or method declared in one file is immediately visible to all other files in that package without import statements.
   - Methods on `app` (e.g. `func (a *app) renderSettings(...)`) can be defined across different files.
2. **First-Class Functions**:
   - In Go, functions are first-class values and can be stored in structs or passed as parameters (e.g. `Build func(c *ui.Context, p palette) ui.Element`).

### Architectural Options (Trade-offs)

#### Option A: Data-Driven Page Descriptor (Struct + Map/Slice)
Define a `Page` struct containing metadata and render callbacks:
```go
type PageID string

const (
    PageMain             PageID = "main"
    PageSettings         PageID = "settings"
    PageAddSubscription  PageID = "add_subscription"
)

type Page struct {
    ID     PageID
    Title  string
    Height func(a *app) float32
    Build  func(a *app, c *ui.Context, p palette) ui.Element
    Wide   bool // supports wide layout
}
```
* **Pros**: Simple to inspect, iterate, and test; minimal boilerplate; easy to register via `map[PageID]Page` or slice.
* **Cons**: Render closures need access to `a *app`.

#### Option B: Interface-Based Page Contract
Define an interface that each page struct satisfies:
```go
type Page interface {
    ID() PageID
    Title() string
    Height() float32
    Render(c *ui.Context, p palette) ui.Element
}
```
* **Pros**: Encapsulates page-specific state inside dedicated page structs (e.g., `type SettingsPage struct { ... }`).
* **Cons**: More boilerplate for simple pages; requires managing how page instances access shared app state and services.

#### Option C: Method-Based Registry on `app`
Keep methods on `*app`, and maintain a dispatch table:
```go
type PageDef struct {
    Title  string
    Height float32
    Render func(c *ui.Context, p palette)
}
// Registered in a map or function on *app
```

### Invariants to Preserve (from `AGENTS.md`)
- **Slide animation (`a.motion.page`)**: The slide animation expects sub-pages to transition in from the right (`(1 - page) * panelWidth`) and main to slide left (`-page * panelWidth`).
- **Page warming (`a.warmed`)**: A new page must be built once in an unseen hold frame (`warmPage`) before the slide animation starts. Do NOT call `ui.NewTester` in production to measure or prewarm.
- **Height settling**: Always call `a.settleHeight()` so the window shell resizes smoothly at rest.

### Testing Strategy
- Use `ui.NewTester(a.view, panelWidth, 520)`.
- Loop over all registered pages in a test: call `a.openScreen(id)`, assert that `a.screen == id`, verify `tester.Frame()` renders without panics, and assert that back navigation restores `PageMain`.

---

## W02 — One RPC Layer in the App (#3)

### Background & Motivation
Direct `a.d.SomeRPC()` calls are scattered across `app.go`, `optimistic.go`, `updates.go`, and `view_pages.go`. Every call handles goroutines, timeouts, gRPC `codes.Unavailable`, and error toasts slightly differently. Centralizing this provides predictable error reporting and clean screen implementations.

### Key Go Concepts & Mental Model
1. **Thread Safety in UI**:
   - gRPC calls take network time and must run asynchronously via `a.async(func() { ... })`.
   - **Crucial Rule**: GUI state changes MUST occur on the UI main thread via `a.post(func() { ... })`. Mutating UI fields directly in a background goroutine causes race conditions (`go test -race`).
2. **Context Deadlines**:
   - Always derive a context with timeout:
     ```go
     ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
     defer cancel()
     ```
3. **gRPC Status & Error Details**:
   - Use `status.FromError(err)` to inspect status codes:
     ```go
     if st, ok := status.FromError(err); ok {
         if st.Code() == codes.Unavailable {
             // Daemon dropped or restarting
         }
     }
     ```

### Architectural Options (Trade-offs)

#### Option A: Dedicated Client Wrapper Struct
Create a wrapper around `pb.DaemonServiceClient`:
```go
type DaemonRPC struct {
    client pb.DaemonServiceClient
    post   func(fn func())
    async  func(fn func())
}

func (r *DaemonRPC) Call(ctx context.Context, fn func(pb.DaemonServiceClient) error) error {
    // handles retries on codes.Unavailable, timeout, logging
}
```
* **Pros**: Isolates gRPC concerns completely; reusable across CLI and GUI; easy to mock in unit tests.
* **Cons**: Slightly more abstraction between callers and protobuf types.

#### Option B: Action Helper with Optimistic Updates
Wrap the common pattern of: (1) apply local state, (2) run gRPC in background, (3) rollback and toast on failure:
```go
type Action struct {
    Name       string
    Optimistic func()
    Rollback   func()
    Run        func(ctx context.Context, d pb.DaemonServiceClient) error
    OnError    func(msg string)
}

func (a *app) performAction(act Action) {
    if act.Optimistic != nil { act.Optimistic() }
    a.async(func() {
        ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
        defer cancel()
        err := act.Run(ctx, a.d)
        if err != nil {
            msg := humanizeRPCError(err)
            a.post(func() {
                if act.Rollback != nil { act.Rollback() }
                a.toastError(msg)
            })
        }
    })
}
```
* **Pros**: Eliminates 80% of repetitive toast/error/concurrency code across settings and switches.
* **Cons**: Requires structuring screen actions into explicit functions.

#### Option C: Stream Coordinator
For streaming RPCs (`WatchStatus`, `WatchTraffic`, `WatchUpdate`), introduce a stream lifecycle manager that automatically reconnects with backoff when the daemon restarts, rather than handling raw reconnection loops in view files.

### Invariants & Pitfalls
- **Dropped daemon mid-call**: Should display one clear toast (e.g., "Daemon is unavailable"), not a burst of stacked toasts.
- **Revision awareness**: When the daemon returns a `revision` or status change, use it to avoid overwriting fresher local optimistic states.

---

## W03 — Additive Daemon API for the Screens (#4)

### Background & Motivation
The UI currently makes an N+1 round-trip: calling `ListSubscriptions`, then calling `GetSubscription` for each one in a loop. Additionally, geo-category lookups parse files repeatedly, and per-server traffic stats lack outbound metrics.

### Key Go & Protobuf Concepts
1. **Protobuf Evolution (Additive Changes)**:
   - In Protobuf, adding optional fields to existing messages is fully backward-compatible.
   - Field numbers cannot be changed or reused.
   - Run `make proto` (compiles `.proto` via `buf`) and `make proto-lint` to verify compatibility.
2. **Go Zero-Values in Proto**:
   - If a boolean field `bool include_configs = 1;` is added to `ListSubscriptionsRequest`, older clients that don't pass it will default to `false` in Go.
3. **In-Memory Caching in Go**:
   - Use `sync.RWMutex` to protect in-memory caches.
   - Check file modification time via `os.Stat(path).ModTime()` to invalidate caches only when the file changes on disk.

### Architectural Options (Trade-offs)

#### For Subscriptions & Configs
- **Option 1: Enrich `ListSubscriptions` (Additive)**
  Add `bool include_configs = 1;` to `ListSubscriptionsRequest`. In `Subscription`, add `repeated SubscriptionConfig configs = 10;`.
  * *Trade-off*: Zero new RPC methods, 100% backward-compatible, completely eliminates the N+1 loop.
- **Option 2: Add `ListNodes` RPC**
  Create a new RPC returning flattened or grouped nodes with subscription info.
  * *Trade-off*: Useful if nodes will later have complex sorting/filtering independent of subscriptions, but introduces a new RPC.

#### For Geo Categories Caching
- **Option 1: ModTime Cache**
  Store `struct { mu sync.RWMutex; lastMod time.Time; categories []string }`. Before parsing geo `.dat` files, inspect `os.Stat`. If unchanged, return the cached slice.
- **Option 2: Lazy Load on Startup / Daemon Watcher**
  Load once when daemon starts, or reload on file change event.

#### For Outbound Traffic Metrics
- Connect daemon to Xray's `StatsService` (`QueryStats` with pattern `outbound>>>...>>>traffic>>>downlink/uplink`). Expose these stats through the existing daemon status or a dedicated telemetry RPC.

### Testing Strategy
- Run `make proto` and `make proto-lint`.
- Write daemon tests in `libs/masir-daemon` testing:
  - `ListSubscriptions` with `include_configs: true` vs `false`.
  - Repeat calls to geo category inspection to verify cache hit.
  - Daemon behaviour when Xray is stopped (`--idle` mode).

---

## W04 — Compact and Wide Layouts (#5)

### Background & Motivation
The app is currently hardcoded to a 300 DIPs width (`panelWidth = 300`) suitable for the macOS tray popover. Advanced features like the upcoming visual Rule Editor, full connection logs, and raw JSON editing cannot fit inside 300px. We need a wide, resizable window while retaining the fast tray panel as the daily driver.

### Key Go & MyGo Mental Model
1. **Window Management**:
   - macOS popovers (`shell_darwin.go`) and standard platform windows (`shell_window.go`) have distinct window traits.
   - Popovers on macOS are governed by AppKit and `patches/mygo-darwin-popover-resize.patch`.
2. **Layout Sizing**:
   - Layout elements in MyGo can be fixed (`.Width(300)`) or flexible (`.FillWidth()`).

### Architectural Options (Trade-offs)

#### Option A: Dual-Window Model (Popover + Auxiliary Window)
- The compact tray panel remains fixed-width (300px).
- An "Advanced..." button or menu item opens a separate, standalone resizable window (`mygo.Window`) with standard window controls (close, minimize, maximize) and a minimum size (e.g., 720x500).
* **Pros**: Cleanest separation. macOS popover doesn't need to morph into a giant floating window (which violates macOS HIG).
* **Cons**: Requires managing lifecycle and synchronization between two window instances.

#### Option B: Morphing Single Window
- The main window resizes between compact width (300px) and wide width (e.g. 700px) when entering wide pages.
* **Pros**: Single window to track.
* **Cons**: Jarring transitions on macOS popover; popover positioning against the menu bar gets awkward when wide.

#### Option C: Layout Context Propagation
Regardless of window strategy, pages should declare their capabilities and adapt:
```go
type LayoutMode int

const (
    LayoutCompact LayoutMode = iota
    LayoutWide
)

type PageView interface {
    Render(c *ui.Context, p palette, mode LayoutMode, width float32) ui.Element
}
```
* Pages that only support wide mode display a placeholder or "Open in Window" button if rendered in compact mode.

### Invariants to Preserve
- **Popover height rules**: `heightForOpen` uses last real `mainHeight` or remembered closed-page height so opening never causes layout jumps.
- **Headless tests**: Tests must verify that all pages can be built at both 300px and 800px width without layout crashes.

---

## W05 — Shared Controls for the New Screens (#6)

### Background & Motivation
Before implementing rules, subscriptions, and log screens, we need reusable UI controls:
- Multi-value chip input (for domain tags, IP addresses, ports)
- Searchable picker with keyboard navigation (geo categories, servers, apps)
- JSON editor with validation and formatting
- Inline banners, progress panels, confirm sheets, empty states

Building them as a cohesive component kit prevents duplicated UI logic and ensures consistent theme support.

### Key Go & GUI Concepts
1. **Component Design in MyGo**:
   - Components are typically functional builders:
     ```go
     func ChipGroup(c *ui.Context, p palette, chips []string, onRemove func(int)) ui.Element
     ```
2. **Keyboard Event Handling**:
   - Check key events on `ui.Context`: ArrowUp, ArrowDown, Enter, Escape, Backspace.
3. **State Management**:
   - Decide whether a component is purely controlled (all state passed in via props/callbacks) or uncontrolled (maintains internal text/selection state).

### Architectural Options (Trade-offs)

#### Option A: Pure Functional Widgets (Matching `widgets.go`)
Write standalone functions taking callbacks:
```go
func SearchPicker[T any](
    c *ui.Context,
    p palette,
    query string,
    items []T,
    filter func(item T, q string) bool,
    renderItem func(c *ui.Context, item T, selected bool) ui.Element,
    onSelect func(item T),
) ui.Element
```
* **Pros**: Idiomatic with current codebase; no state struct synchronization.
* **Cons**: Long parameter lists if many customizations are needed.

#### Option B: Component Configuration / State Struct
Define a struct to configure and hold temporary state:
```go
type ChipInputState struct {
    Chips       []string
    CurrentText string
    Placeholder string
    OnAdd       func(string)
    OnRemove    func(index int)
}

func (s *ChipInputState) View(c *ui.Context, p palette) ui.Element { ... }
```
* **Pros**: Clean API; options are named fields; easy to add defaults.
* **Cons**: Caller must retain and manage the state struct instance.

#### Option C: Functional Options Pattern
For complex controls like the JSON Editor or Searchable Picker:
```go
func NewJSONEditor(opts ...JSONEditorOption) ui.Element
```

### Component Breakdown
1. **Chip Input**:
   - Horizontal wrap flow with text field at the end.
   - Backspace on empty input highlights/deletes the last chip.
   - Enter or comma adds the chip.
2. **Searchable Picker**:
   - Search text box with sticky list header.
   - Virtualized row list (`ui.List`) for performance with thousands of items.
   - Keyboard cursor highlight (Up/Down) + Enter selection.
3. **JSON Editor**:
   - Monospaced text area.
   - "Format / Prettify" button (`json.Indent`).
   - Inline error banner when JSON parsing fails (`json.Valid` or `json.Unmarshal`).
4. **Layout Test Helper**:
   - A helper function for tests:
     ```go
     func assertRendersWithoutClipping(t *testing.T, elem ui.Element, width, height float32)
     ```

### Testing Strategy
- Unit test JSON validation and formatting logic.
- Headless UI tests (`ui.NewTester`) for keyboard selection in the picker.
- Snapshot tests verifying light and dark palette rendering.
