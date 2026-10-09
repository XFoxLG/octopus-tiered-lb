# Model Market & Pricing

### 💎 Model Market & Pricing

The `Model` route (Model Market) provides three switchable toolbar views: **Market** (pricing and coverage cards), **Available Endpoints** (endpoint → model grouping), and **Price Categories** (fallback pricing rules plus peak/off-peak billing).

**Market view data merged on each card:**

- Custom or synced pricing from the LLM price catalog (input / output / cache read / cache write)
- Channel coverage and enabled key counts from channel-model relationships
- Average latency, success rate, and success / failure request counts from recorded model stats
- A peak-billing badge when the model is covered by a peak/off-peak billing schedule (directory price is the peak price; the idle price is discounted)

**Multi-dimension filtering (Market view):**

- Name search shared with the toolbar search box
- Capability chips (chat, embeddings, rerank, …) — conversation-style endpoints count toward the chat capability
- Vendor chips inferred from the model name
- Pricing filter: all / priced / free
- Normalized-name dedupe: merges naming variants of the same base model (e.g. `kimi-k2.5`, `moonshotai/kimi-k2.5`, `dmxapi-kimi-k2.5-cc`). Rules come from the Settings `Normalize` card (router prefixes, functional suffixes, explicit variant→canonical mappings), and dedupe can be turned on by default via a dedicated setting
- The collapsible filter bar shows the live result count (visible / total), the number of active filters, and a one-click reset that clears search, capability, provider, pricing, and dedupe

**Summary metrics:**

The toolbar summary strip shows four metrics plus the last price-update time and a refresh-prices button. The numbers come from the market endpoint's summary over the **full** dataset — they do **not** change with search or filters (only the card list is filtered). When the default-dedupe setting is on, the market endpoint itself aggregates naming variants server-side, so cards and summary counts already show merged entries:

| Metric | Meaning |
|--------|---------|
| Models | Total models aggregated in the market |
| Channel Coverage | Total channel-to-model coverage entries across the market |
| Unique Channels | Distinct channels across all models |
| Average Latency | Request-weighted average latency across the market |

**Cards and dialogs:**

- Responsive virtualized cards (grid / list / compact layouts on desktop, single-column list on mobile), sorted by success rate or request count
- Expand a card for price pairs (input / cache-read and output / cache-write, with optional CNY conversion), runtime metrics, and per-channel rows (enabled state and enabled key count); channel tags fold into a `+N` chip when there are too many
- Standard edit dialog with strict price validation (non-negative decimals; invalid fields are highlighted inline instead of being silently zeroed) and a delete confirmation dialog

**Available Endpoints view:**

Aggregated from valid route groups and inverted into endpoint → model groups. Conversation-family endpoints (chat / deepseek / mimo / responses / messages / auto) are merged into a single "Chat" group shown first; models with no declared endpoint fall under the auto (`*`) group. This view shares the toolbar search box with the market view — a search matches endpoint names/labels or model names, forces all groups expanded, and reveals every matched chip. Each group card is collapsible with a model count, vendor-icon chips, and a "show more" control for large model sets.

**Price Categories view:**

- Fallback pricing by rule for models without an exact price; rules are matched in sort order and the first hit wins
- Rule table: name, rule type (exact / prefix / contains), rule value, the four prices, sort order, and an enabled badge
- Create / edit dialog with validation: name and rule value required, prices must be finite non-negative numbers, sort order must be an integer. Errors appear after the first submit attempt and clear as the input is fixed
- Peak/Off-peak Billing section (currently for DeepSeek): off-peak multiplier, weekend off-peak switch, and two Beijing-time windows (an empty window is closed; both windows closed means all-day off-peak; overlapping windows are allowed with a hint)
- Rule changes invalidate the market price cache; this view has no search box

**Query gating:**

Views stay mounted when you switch pages (keep-alive), so every query is gated: the market and capabilities queries only run while the Model module is active and the matching view is selected, and switching views stops the query for the view you left.

**Data Sources:**

- The system periodically syncs model pricing data from [models.dev](https://github.com/sst/models.dev)
- When creating or syncing channels, if a model is not yet in the local catalog, Octopus automatically creates a local model-price record so the price can still be maintained manually
- Manual creation of models that exist in models.dev is also supported for custom pricing

**Price Priority:**

| Priority | Source | Description |
|:--------:|--------|-------------|
| 🥇 High | This Page | Prices set by user in the model market page |
| 🥈 Low | models.dev | Auto-synced default prices |

> 💡 **Tip**: To override a model's default price, simply set a custom price for it in the model market page.

**Operational actions preserved on the page:**

- Create a custom model price record
- Edit input / output / cache prices for an existing model
- Delete a custom model entry
- Refresh upstream pricing from the toolbar summary strip
- Keep the scheduled price refresh policy in the Settings `LLM Sync` card

---

| [← Home](../Home.md) | [← Previous](05-Groups.md) | [Next →](07-Relay-Endpoints.md) |
