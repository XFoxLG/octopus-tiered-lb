# Octopus Web Console

This directory contains the management UI for Octopus.

## Stack

- Next.js 16
- React 19
- TypeScript
- Tailwind CSS 4
- TanStack Query
- Zustand 5
- Radix UI
- `next-intl`

The app uses App Router as the shell entrypoint, but the actual screen switching inside the console is handled client-side in `src/components/app.tsx`.

## Commands

Install dependencies:

```bash
pnpm install
```

Run the frontend against a local backend:

```bash
NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080" pnpm dev
```

Lint:

```bash
pnpm lint
```

Build the static export used by the embedded management UI:

```bash
NEXT_PUBLIC_APP_VERSION="$(git describe --tags --always 2>/dev/null || printf 'dev')" pnpm build
```

## Environment Variables

- `NEXT_PUBLIC_API_BASE_URL`: Optional API base URL. Defaults to relative requests against the current origin.
- `NEXT_PUBLIC_APP_VERSION`: Version string shown in the UI. For release builds, set this to the current git tag or commit.

## Output and Embedding

`pnpm build` produces a static export in `out/`.

The Go server embeds these files from `../static/out/`. A typical local embed flow is:

```bash
pnpm install
NEXT_PUBLIC_APP_VERSION="$(git describe --tags --always 2>/dev/null || printf 'dev')" pnpm build
cd ..
mkdir -p static/out
cp -r web/out/* static/out/
```

If `static/out/_not-found/` exists but is empty, add `.keep` before running `go build` or `go run main.go start`.

The top-level `Dockerfile` already builds this frontend and copies the export into `static/out` during image build, so release images contain the matching frontend automatically.

## Key Directories

- `src/components/app.tsx`: Main application shell with login mode switching (user credentials / API key)
- `src/components/modules/home/*`: Runtime/version overview, hero summary, trend chart (multi-metric overlay: cost/count/tokens/success-rate), GitHub-style activity heatmap, ranking panel, and analytics overview cards
- `src/components/modules/remote-site/*`: Hub module — tab-based interface with 5 sub-panels: Sites (multi-account cards with inline balance / sync / check-in status, archive/restore, batch edit, bulk import), Site Channels (SiteChannelSection), Automation (SettingSiteAutomation), Balance (plan balance charts), and TokenPlan (token plan monitoring). Slimmed from the previous 7-tab layout; check-in/announcement/redemption/usage/credential panels are inlined into the Site cards or accessed via the API
- `src/components/modules/site/*`: Site management module — the multi-account site card grid rendered inside the Hub "Sites" tab. Includes `Site` (main grid + site/account dialogs), `CheckinPanel` (summary strip), `AccountEditDialog`, `SiteEditDialog`, `checkin-status`, `site-message`, and `ui-store`. Supports pin / archive / restore, batch enable/disable/delete, and bulk import from AllAPIHub / MetAPI
- `src/components/modules/site-channel/*`: Site Channels section — dedicated view for managing channels associated with remote sites (projected channel bindings)
- `src/components/modules/credential/*`: Shared `CredentialDialog` component reused by the Hub CredentialPanel
- `src/components/modules/model-mapping/*`: Model name mapping rules UI (exact/wildcard/regex pattern-based name rewriting with priority and group scope). Not registered as a top-level nav route; backed by `/api/v1/model-mapping`
- `src/components/modules/proxy-pool/*`: Proxy configuration pool management — CRUD, connectivity testing, reference tree tracking, and jump-to-reference navigation. Accessible from the app shell toolbar
- `src/components/modules/channel/*`: Channel configuration with 9 built-in templates (OpenAI, OpenAI Responses, Anthropic, Gemini, DeepSeek, OpenRouter, SiliconFlow, Volcengine, MiMo), key management, sync, latency, model declarations, proxy mode, and request rewrite profiles
- `src/components/modules/group/*`: Route groups, balancing strategy configuration, zashboard-style collapsible group list, group testing, single-group AI route append flow, AI route progress dialog, endpoint provider configuration, and CC Switch deep link generator (Claude Code, Codex, Gemini, OpenCode, OpenClaw)
- `src/components/modules/model/*`: Model Market UI with dual-tab interface (Market and Capabilities) plus an Available Endpoints view (migrated from the API key page), including summary strip, virtualized cards, multi-dimension filter (capability + provider + normalized-name dedupe), price-edit actions, and capabilities panel with endpoint support and status badges
- `src/components/modules/analytics/*`: Six tabs (Channel × Model [default], Usage Breakdown, Route Health, Latency, Evaluation, Cache) with usage-distribution share chart, latency distribution histogram, provider prompt cache analytics, and share snapshot (PNG export / clipboard copy via html-to-image)
- `src/components/modules/log/*`: Relay request list/detail views, usage/cost display, and error diagnostics
- `src/components/modules/notification/*`: Unified notification center — top-level route aggregating system events, alert firings, and plan notifications. Four groups: Messages (inbox / archived), Alerts (rules / history via `AlertSections`), Delivery (channels / policies / preferences), and Reports (schedules / history). Renders notification text via `notif-text.ts` and streams updates over SSE
- `src/components/modules/plan-provider/*`: Plan provider monitoring section — tracks upstream subscription quota/usage (Codex, MiMo, StepFun, SenseNova, and balance-type providers) and manages auto-created forwarding channels
- `src/components/modules/report/*`: Usage report scheduling (`ReportScheduleManager`) and report history (`ReportHistoryList`), rendered inside the Notification reports group
- `src/components/modules/alert/*`: Alert rule, notification-channel, and history UI. No longer a top-level route — its `AlertSections` component is embedded inside the Notification module's Alerts and Delivery groups
- `src/components/modules/ops/*`: Six tabs (Telemetry, Quota, Health, Maintenance, System, Audit). Telemetry includes hero metrics, P95 latency, throughput RPS, database health, session/quota activity, provider health table (sortable columns + mini bar charts), and provider prompt cache analytics. Maintenance tab consolidates Retry / Circuit Breaker / Response Filter settings (moved out of Settings page)
- `src/components/modules/apikey/*`: API key creation, allowlists, expiry, max-cost, RPM/TPM, per-model quota, and IP/CIDR allowlist controls
- `src/components/modules/apikey-dashboard/*`: API key dashboard views — request stats, token usage, cost, quota, expiration countdown, and supported models when authenticated via API key
- `src/components/modules/setting/*`: Settings cards for Info (version, self-update, version mismatch detection), Appearance (theme, locale, nav order + visibility), AI Route, Auto Strategy, Account (timezone, session), Log, System (CORS allowlist, proxy, stats interval), LLM Sync, Backup (export/import), Redis (connection identification, PING, encrypted per-service persistence on Render, restart to apply), WebDAV Backup, WebAuthn/Passkey, Normalize (model-name normalization rules), and the Proxy Pool dialog. Supports drag-and-drop card reordering with localStorage persistence. Retry/CircuitBreaker/ResponseFilter live in `ops/Maintenance.tsx`; group maintenance actions remain in the Group module.
- `src/components/modules/user/*`: Management-console user and role administration
- `src/components/modules/navbar/*`: Top-level navigation state and persisted nav-order/visibility helpers
- `src/components/modules/toolbar/*`: Shared toolbar with per-page search, layout toggle (grid/list), sort options, and context-dependent filters
- `src/components/modules/login/*`: Login form supporting both username/password and API-key authentication modes behind a tabbed interface
- `src/components/modules/logo/*`: Shared animated Octopus logo component used by login and first-run screens
- `src/components/modules/first-run-setup.tsx`: Bootstrap wizard for creating the initial admin account when no admin exists, with animated particle background
- `src/api/`: API client and endpoint hooks (TanStack Query)
- `src/route/config.tsx`: UI route registration (lazy-loaded top-level modules: home, hub, channel, group, model, analytics, log, notification, ops, apikey, setting, user)
- `src/stores/`: Zustand state stores
- `src/lib/`: Utilities, i18n, logger, time zone helpers (10 time zones), service worker management
- `public/locale/`: Localized text resources (en, zh_hans, zh_hant)

## Notes

- The settings module supports drag-and-drop reordering, with order persisted to localStorage. A "Reset to Default" button restores the original order. Retired section IDs are ignored without resetting other preferences.
- Group mode labels and endpoint type display values are shared with backend behavior and should be updated together when adding new strategies or capabilities.
- AI routing has two entry points: the route page button generates the full routing table, while the group edit dialog button appends matched items into the current group only.
- The settings field for AI routing is now the default target group for the single-group compatibility flow, not the target for full-table generation.
- The `Model` route is now a `Model Market` view with dual tabs (Market and Capabilities) plus an Available Endpoints view (migrated from the API key page), backed by `/api/v1/model/market`; it merges pricing, coverage, enabled-key counts, latency, and success metrics while preserving price-management actions. The Capabilities tab shows per-model endpoint support, conversation flags, and availability status. The Market view supports multi-dimension filtering (capability + provider + normalized-name dedupe).
- `Analytics` is organized into six tabs: `Channel × Model` (default, with usage-distribution share chart), `Usage Breakdown` (renamed from Utilization, with no-billing hint), `Route Health`, `Latency` (with P50/P95/P99 metrics and distribution histogram), `Evaluation` (group testing and AI routing entry points), and `Cache` (provider prompt cache). The overview query remains available, but its primary UI summary cards now live on Home.
- `Ops` is organized into six tabs: `Telemetry` (hero metrics, P95, provider health table with sortable columns + mini bar charts, prompt cache analytics), `Quota` (merged with API key detail — total tokens + success rate + jump), `Health` (with jump to Analytics → Route Health), `Maintenance` (Retry / Circuit Breaker / Response Filter, moved out of Settings), `System`, and `Audit`. Audit only covers selected management write routes, not public relay traffic.
- Semantic answer caching is retired. Its settings, runtime calls and live metrics are removed; historical log hit markers and shared database values remain readable. Ordinary embeddings, rerank, AI routing and provider prompt caching remain available.
- Redis URL input never displays saved credentials. An empty replacement preserves the saved connection; a new URL replaces its identity without inheriting an old password, database or TLS switch. Recognition, temporary PING, persistence and runtime status are distinct. Render persistence is encrypted and scoped by backend `RENDER_SERVICE_ID`; explicit cache environment configuration disables web saving. Saving never switches the running backend. See [deployment and shared-database guardrails](../docs/deployment-cache-and-chat.md).
- Top-level page order and visibility are edited inside the `Appearance` card, persisted through the `nav_order` and `nav_visible` settings, and normalized against `DEFAULT_NAV_ORDER`, so missing routes are appended automatically and unknown routes are dropped.
- The Hub (`remote-site`) module is a tab-based page with 5 tabs: Sites, Site Channels, Automation, Balance, and TokenPlan. The Sites tab renders the `site/` module's multi-account card grid with inline balance / sync / check-in status, archive/restore, batch edit, and bulk import. The Automation tab renders the `SiteAutomation` settings component. The Balance and TokenPlan tabs render plan-provider monitoring. The previously separate announcement / checkin / redemption / usage-history / credential panels were inlined into the Site cards or are accessed via the API.
- The `model-mapping` module provides pattern-based model name rewriting rules but is not exposed as a top-level navigation route. It is accessible from the app shell toolbar.
- The `proxy-pool` module provides named proxy configuration management with CRUD, connectivity testing, and reference tracking. It is accessible from the app shell toolbar.
- The login screen supports two authentication modes (user credentials and API key) behind a tabbed interface, and the first-run bootstrap wizard appears automatically when no admin account exists. Both the login screen and user management support WebAuthn/Passkey registration and authentication.
- The API key dashboard shows a dedicated view when authenticated via API key (instead of username/password), displaying request stats, token usage, cost, quota, and expiration info.
- The Settings `Backup` card supports application JSON export/import. Deployment-owned Redis credentials are excluded and preserved during restore; this is not a full database recovery image or a cross-database migration tool.
- The Settings `WebDAV` card provides automated cloud backup via WebDAV with configurable schedule, remote file management, and one-click restore.
- The Hub `Automation` tab (formerly the Settings `Site Automation` card) configures auto-sync and auto-checkin intervals for remote sites.
- The `channel` module includes per-channel proxy mode (direct/system/pool/inherit), request rewrite profiles (preserve/openai_chat_compat/codex with codex header, tool role, and system message strategies), param_override JSON for per-channel parameter injection into outbound requests, and a per-channel `skip_model_test` toggle (issue #98) to exclude the channel from group/model availability probes.
- The `group` module includes endpoint provider configuration (openai/deepseek/mimo/siliconflow/newapi) for stripping incompatible reasoning fields, outbound format with `chat_only`/`responses_only` modes that disable cross-format adapter fallback, supports a zashboard-style collapsible group list view, and includes a "Maintenance" dropdown button (Purge Unavailable Models, Delete All Route Groups) relocated from the Settings page.
- The Home page includes a GitHub-style activity heatmap visualizing request activity across the past year, and a multi-metric overlay chart (cost/count/tokens/success-rate selectable simultaneously).
- The Analytics page includes a Share button that generates a PNG snapshot of the current analytics state for download or clipboard copy.
- User-configurable time zone (10 zones) affects all date/time display in the UI, independent from server-side stats timezone.
- Viewer accounts see masked domains (`***`) for Hub-related management data across sites, remote sites, credentials, channels, and URL settings.
- When backend API surfaces or top-level modules change, update both `web/README.md` and the root README files so the embedded-console docs remain consistent.
- The Settings page now has 14 cards (down from 18, plus the Normalize card for model-name normalization rules and the Redis card for the optional cache backend). Response Filter, Log Level, and WebAuthn/Passkey remain; Retry/CircuitBreaker moved to Ops Maintenance; Site Automation moved to Hub Automation; PurgeUnavailableModels/RouteGroupDanger moved to Group Maintenance button.
- The `notification` module is the unified notification center and a top-level route; the former standalone `alert` route was removed and its UI now lives inside the Notification module's Alerts and Delivery groups.
