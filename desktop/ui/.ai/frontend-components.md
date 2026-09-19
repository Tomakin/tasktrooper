# Frontend Components (Atomic Design)

The web UI (`src`) follows **Atomic Design**. Every piece of UI is one of: **atom → molecule → organism → template → page**. This is a hard rule for all UI work.

## The Rule (read before writing any UI)

1. **Reuse first.** Before writing markup, find the existing atom/molecule/organism that fits. Never hand-roll a button, card, badge, input, dialog, header, empty state, list row, etc. — import the shared component.
2. **No ad-hoc equivalents.** If you catch yourself writing `<div className="rounded-lg border p-4">` that duplicates `Card`, or a bare `<button className="...">` that duplicates `Button`, stop and use the component. Raw elements are only for genuinely one-off layout wrappers (`div`, `section`) with no shared equivalent.
3. **New shared component?** If a pattern repeats 2+ times and no component covers it, create one at the correct atomic level (see placement below) and use it everywhere the pattern appears.
4. **Updating a shared component is allowed, but must not break callers.** See "Safe updates" below.
5. Every new page composes molecules/organisms inside a template (layout); it does not re-implement shells, sidebars, or headers.

## Levels & Placement

| Level | What it is | Lives in | Examples |
|-------|-----------|----------|----------|
| **Atom** | Single-purpose primitive, no business logic, style-only | `components/ui/` | `button`, `card`, `badge`, `input`, `label`, `checkbox`, `switch`, `select`, `textarea`, `dialog`, `separator`, `skeleton`, `spinner`, `progress`, `scroll-area`, `empty-state` |
| **Molecule** | Small composition of atoms, reusable, little/no state | `components/admin/`, `components/layout/`, `components/markdown/`, `components/attachments/`, feature dirs | `PageHeader`, `FormDialog`, `KeyValueEditor`, `MultiSelectPicker`, `ToolPolicyForm`, `PageContent`, `SidebarNavLink`, `HealthStatus`, `MarkdownContent`, `MarkdownField`, `ActivityFeedItem`, `WizardStepper`, `TypingIndicator`, `ClarificationCard`, `ClarificationSummary`, `SessionActionCard`, `ProjectIndexStatus`, `RepositoryRow`, `TaskRunSteps`, `AgentStepList` (+ the graph node parts in `chat/AgentSteps.tsx`), `confirm-dialog`, `AttachmentDropzone`, `AttachmentList`, `setup/SetupShell`, `setup/SetupStepList`, `setup/DesktopOnlyNotice`, `runner/BlockerNotice`, `projects/ProjectFormDialog`, `admin/StoreCredentialForm` (per-provider credential block, exported from `StoreCredentialsSection.tsx`) |
| **Organism** | Larger, often stateful feature block; composes molecules+atoms | feature dirs (`chat/`, `board/`, `workspace/`, `agent/`, `projects/`, `admin/`, `runner/`, `setup/`) | `Composer`, `MessageList`, `SessionSidebar`, `ActivityPanel`, `PlanView`, `SessionGraphView`, `CreateTaskDialog`, `TaskDetailDrawer`, `TaskAssigneeFields`, `ChatTaskDrawer`, `RepositoryDialogs`, `ProjectRepositoriesSection`, `ActivityFeed`, `NewAgentDialog`, `NoProjectsNotice`, `AgentKPISection`, `MCPServerForm`, `admin/VercelCard` (Vercel token connection + per-app hosting links), `runner/LocalCliCard`, `runner/EnvironmentPreflight` (+ the pure `runner/claudeCodeConnect.ts` connect-flow helper), `setup/EnvironmentStep`/`ClaudeCodeStep`/`GitHubStep`/`FirstProjectStep`, `admin/GitHubCard`, `projects/useRepositoryImport` (+ its `RepositoryImportDialogs`/`ErrorNotice`), `admin/AppStoreConnectCard`, `admin/GooglePlayCard`, `admin/StoreAppPickerDialog` (+ `StoreAppsBrowser` and the `useStoreAppListing` hook, same file), `projects/MobileStorePanel`, `operations/StoreReleaseControls` (store channel vocabulary + `ChannelPromoteButton`), `admin/GoogleCloudCard`, `admin/VercelProjectPickerDialog` (+ `useVercelProjectListing`), `admin/GCloudResourcePickerDialog` (+ `GCloudResourcesBrowser`, `GKEWorkloadsNotice`, `useGCloudResourceListing`), `projects/VercelProjectPanel`, `projects/GCloudResourcePanel` |
| **Template** | Page shell / layout that arranges organisms; provides sidebar, header, routing outlet | `components/layout/` | `WorkspaceLayout`, `WorkspaceShell`, `WorkspaceSidebar`, `WorkspaceAgentLayout`, `SettingsLayout`, `Header`, `ProtectedRoute` |
| **Page** | Route target; loads data, composes organisms in a template | `pages/` | `BoardPage`, `AgentChatPage`, `BoardSettingsPage`, `SetupPage`, `ProjectsPage` (also lists each project's repositories), `IntegrationsSettingsPage`, … |

Placement rule: **atoms only in `components/ui/`**; molecules/organisms in the closest feature directory (`chat/`, `board/`, `workspace/`, `agent/`, `projects/`, `admin/`, `runner/`, `setup/`) or `layout/` for structural pieces; pages in `pages/`.

## Guided first-run sequence — `/setup`

| Piece | What it is |
|---|---|
| `lib/setup.ts` | Step ids, the 3 states (`done`/`todo`/`unknown`), `activeSetupStep`/`setupComplete`/`setupNeedsWork`/`setupStepUnlocked`, `SETUP_PATH` |
| `hooks/useSetup.tsx` | `SetupProvider` (above the router, in `App.tsx`) deriving all four steps; owns the gate policy as `redirectToSetup` |
| `pages/SetupPage.tsx` | `setup/SetupShell` + `setup/SetupStepList` + the active step's body |

- **Nothing is stored.** Each step is read back from the thing itself:
  `host.preflight().ready`, the connected `claude` CLI row, `GET /v1/settings/github`,
  and a project with ≥1 linked repository. A local "step done" flag would be wrong the
  moment anything is disconnected.
- **Reuse, not reimplementation:** `runner/EnvironmentPreflight` (`onReport`),
  `runner/claudeCodeConnect` (+ `connectFailure`, and `connectStepLine` exported from
  `LocalCliCard`), `admin/GitHubCard`, `projects/useRepositoryImport` +
  `ProjectRepositoriesSection` + `ProjectFormDialog` (all shared with `ProjectsPage`).
- **Browser:** only step 1 is `actionable: false` (the preflight probes the machine
  through the desktop bridge) → `setup/DesktopOnlyNotice`, never a dead button.
  `unknown` never renders as "not done"; the failing call's own sentence is shown.
- **Entry:** `ProtectedRoute` redirects on `redirectToSetup` (`needsWork`, not
  dismissed, after a 6 s grace so a launching supervisor is not read as "never
  started"), carrying `location.search`. `WorkspaceSidebar` shows "Finish setup"
  while `needsWork`. "I'll do this later" writes only `uiCache`'s `setup.dismissed`
  — a dismissal, never progress.

## Task assignee

| Piece | What it is |
|---|---|
| `board/TaskAssigneeFields.tsx` | Organism: the agent picker (`Bot`); renders nothing when the board has no agents. |

- Used by `CreateTaskDialog` and `TaskDetailDrawer`'s sidebar (so `ChatTaskDrawer`/`ReleasedPage`
  too). `BoardPage`'s card shows the agent badge; `created_by` shows when no agent is set.
- Clearing the assignee sends `null`.

## Concurrent agents

| Piece | What it is |
|---|---|
| `admin/AgentConcurrencyCard` | Organism on `SettingsPage`: 1–10 picker and the live "active / limit, queued" badge (`/v1/settings/agent-concurrency`, refreshed every 10 s); renders nothing on a server without the route |

## Store console + mobile release panel

| Piece | What it is |
|---|---|
| `admin/StoreCredentialForm` | The per-provider credential block, exported from `StoreCredentialsSection.tsx` (the old `StoreCredentialsSection` export is gone; callers render one `StoreCredentialForm` per provider). Used by `AppStoreConnectCard` / `GooglePlayCard`. |
| `admin/StoreAppPickerDialog` | Dialog to bind a repo/platform to one store app; same file also exports `StoreAppsBrowser` (lists a credential's apps) and the `useStoreAppListing` hook (on-demand fetch, `listing_available:false` ≠ error). |
| `projects/MobileStorePanel` | Organism: per-repo link/tracks/promote/build panel, composes `StoreAppPickerDialog` and the pieces below. |
| `IntegrationsSettingsPage` | Composes `AppStoreConnectCard` + `GooglePlayCard` + `GoogleCloudCard` alongside `GitHubCard`/`VercelCard` (both pasted tokens). |
| `projects/VercelProjectPanel` | Organism: the frontend scope's bound Vercel project — production URL, latest deployment, and the last failed one kept separately. Composes `VercelProjectPickerDialog`. |
| `projects/GCloudResourcePanel` | Organism: the backend/worker scope's bound Cloud Run service or GKE cluster. Composes `GCloudResourcePickerDialog`. |

Three provider pickers, one shape. `StoreAppPickerDialog`, `VercelProjectPickerDialog`
and `GCloudResourcePickerDialog` all answer the same three states and must keep
doing so: a list, "connected but this account cannot be enumerated" (which opens
the manual identifier field), and **not connected at all** (which opens neither —
there is nothing to verify a typed value against — and points at Integrations).
An empty list is a fourth, separate answer. The server states which one it is in
`reason`; never infer it from an empty array.

The store channel vocabulary (`STORE_CHANNELS`, `NEXT_STORE_CHANNEL`, the
label/badge helpers, `ChannelPromoteButton`) lives in
`operations/StoreReleaseControls` and is imported by `MobileStorePanel` —
do not redeclare that switch elsewhere.

## Standard building blocks (use these, don't reinvent)

- **Page heading + actions** → `admin/PageHeader` (title, description, `action` slot).
- **Page body wrapper / max-width + spacing** → `layout/PageContent` (pass `className="space-y-6 pb-8"` for multi-section pages).
- **Buttons** → `ui/button` (`variant`, `size`, `asChild`). Never a raw styled `<button>` except tiny inline icon toggles.
- **Cards / panels** → `ui/card`.
- **Status pills** → `ui/badge`.
- **Forms** → `ui/input`, `ui/label`, `ui/select`, `ui/switch`, `ui/checkbox`, `ui/textarea`; dialogs via `ui/dialog` (+ `DialogHeader/Footer/Title`) or `admin/FormDialog`.
- **Destructive confirm** → `ui/confirm-dialog`.
- **Empty states** → `ui/empty-state`.
- **Loading** → `ui/skeleton` / `ui/spinner`.
- **Binary attachments (images/documents)** → `attachments/AttachmentDropzone` (hidden input + button, drag-over highlight, paste-to-upload; `variant="button"` for a bare picker button; exports `uploadAttachmentFiles`) and `attachments/AttachmentList` (image blob thumbnails / file chips, click-to-open, optional `onRemove`, `compact` for chips). Never point `<img src>` at `/v1/attachments/{id}` — it needs the Authorization header; use `attachments/useAttachmentBlob`.
- **List rows** → bordered `divide-y` container with row `px-4 py-3` (see `BoardSettingsPage` columns list) — extract to a molecule if it recurs.
- **Sidebar links** → `layout/SidebarNavLink`.

## Data loading (page fetches)

| Rule | How |
|------|-----|
| Never blank a page on refresh | `setLoading(true)` only on first mount — use `useFirstLoad(...keys)` (`hooks/useCachedState`) |
| Paint before the network answers | Hold fetched payloads in `useCachedState(key, fallback)`; keys for board/backlog data live in `lib/project-board` |
| Failed refresh | Toast and keep the last good data; never `setX([])` |
| Board-shaped lists | Merge with `mergeTaskList` so unchanged rows keep object identity (an open drawer refetches on `task` identity change) |
| Live updates | `usePolling(fn, ms, enabled)` — board 5s, backlog 8s; polls pause on a hidden tab |
| Mutations | Update local state first, send the request, replace with the server's answer, revert on error; bump a version ref so an in-flight refresh started before the change is discarded |
| Request deadlines | `api.ts` times out reads at 20s and writes at 180s |

## Safe updates (change a shared component without breaking the app)

Shared components have many callers. When editing one:

1. **Prefer additive, backward-compatible changes.** New props must have defaults; do not change existing prop names, types, or default behavior.
2. **Before any breaking change** (rename/remove a prop, change a default, change DOM/markup that callers style): run `grep -rn "<ComponentName" src` (and the import) to list every caller, and update all of them in the same change. Build + typecheck must pass.
3. Style-only tweaks are usually safe, but check that no caller depends on the old spacing/size (e.g., a caller that added compensating margins).
4. Keep the component's responsibility single. If a change only serves one caller, add a prop (opt-in) rather than changing the default for everyone.
5. After editing, `npx tsc --noEmit` and `npm run build` must be green — CI runs exactly those two.

## Where this is enforced

This file is linked from `CLAUDE.md`. Any UI task must follow it: reuse existing components, place new ones at the right level, and update shared components safely.
