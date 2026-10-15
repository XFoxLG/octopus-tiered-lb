# 审计：Higress ai-proxy（Claude→OpenAI 转换器）

- 源：`alibaba/higress` @ `c74d7e2d`（Apache-2.0），本地只读副本 `_diagnostics/conformance-research/higress/`
- 审计范围：`plugins/wasm-go/extensions/ai-proxy/provider/claude_to_openai.go`（1268 行，单文件覆盖请求/响应/流式三个方向）+ `claude.go` 的调用面
- 定位：经验来源（WASM 插件形态与我们不同，但其协议细节处理是四源里「Claude 客户端体验」最完整的之一）。

## 结论摘要

1. **请求方向**的处理链已经很成熟：system（字符串/数组）→ 头部 system 消息；tool_use/tool_result → tool_calls/tool 消息；图片 base64/URL → `image_url`；thinking → `reasoning_effort` 阈值映射，且**非标准字段一律不发给严格 OpenAI 上游**（存为 `ClaudeThinking` 交给专门的 provider 适配器）。
2. **流式方向**是本文件最厚的部分：content block 动态索引分配、thinking→text→tool 的块序维护、**工具块串行化**（Claude 要求顺序 tool 块，OpenAI delta 可能交错到达时先缓存参数再逐块发出）、pending stop_reason 与 usage 合流、`[DONE]` 兜底关块、重复 usage 块忽略。
3. 两个「细小但值钱」的技巧：① 剥离 system 文本里 `x-anthropic-billing-header` 的 `cch=` 动态字段（值每次请求都变，会**破坏 prompt cache**）——我方 v2.9.1 已采用同一口径；② `computeClaudeInputTokens` 处理 OpenAI `prompt_tokens` 含 cached tokens 与 Claude `input_tokens` 不含的口径差。
4. 请求侧对「无效回合」有兜底：只含 reasoning 块的回合降级为空可见内容（`Content = ""`），避免 strict 上游拒绝 role-only 消息。
5. 与我们重叠度很高（我们的 outbound 已有 `stream_options.include_usage` 注入、thinking→effort 映射、图片 data URL 转换），真正的差距：**cch 剥离**（v2.9.1 已补，见差距 1）与**工具块串行化的细节完备度**（二期流式对拍）。

## 差距清单

### 1. `cch=` 动态字段剥离（提示缓存保命） [fix-p1]

- 样本/出处：`stripCchFromBillingHeader`（`claude_to_openai.go:1230–1267`），注释：cch 每次请求都变，会破坏 prompt caching
- 修复前行为（我们，v2.9.1 之前）：全仓库无 `cch` 处理（grep 0 命中）；Claude Code 类客户端把 `x-anthropic-billing-header: ...; cch=...` 作为 system 文本块传入时，我们原样转发，**上游每次看到的 system 都不同** → 前缀缓存失效 → 计费与首字都受损（与我们实测「输入 88–110K、缓存命中率对首字影响巨大」同一机制）。
- 目标行为：在入站（anthropic）解析或出站（chat/anthropic/responses）序列化前，对 `x-anthropic-billing-header:` 前缀的文本块做 `; cch=...` 剥离。
- 优先级：P1（省钱 + 首字；改动极小）。
- triage：**已修**——v2.9.1 fix-p1：`internal/transformer/inbound/anthropic/billing_header.go` 纯函数（只剥 `; cch=…`，保留块与其余字段；ASCII 大小写不敏感、支持中段/末尾/多次）+ 单测 + 集成用例；样本 `scenario-billing-header-cch`（真实 Claude Code system 数组形态脱敏）入库并通过三方对拍登记（axonhub 不处理该字段、CLIProxyAPI 整块丢弃，均已在 `conformance/deviations.json` 记录为 no-change 策略差异）。

### 2. 工具块串行化与流式块序完备度 [fix-p2]

- 样本/出处：`startToolCall`/`buildClaudeStreamResponse`（工具块 active 索引、cached arguments、`input_json_delta` 只在 active 时发出、finish_reason 时按序补齐）
- 当前行为（我们）：`scenario-parallel-tool-pairing` 非流式对齐；流式并行工具的**块序**我们未对拍（一期只做 request 方向；流式对拍在第二阶段）。
- 目标行为：保持「Claude 顺序块」约束；参数先缓存、成块发出。
- 优先级：P2（待流式对拍基建就绪后验证；可能已正确）。
- triage：**挂起**——二期流式对拍的第一批样本之一（`scenario-parallel-tool-pairing-stream`）。

### 3. reasoning-only 回合的降级策略 [fix-p2]

- 样本/出处：`ConvertClaudeRequestToOpenAI`（`openaiMsg.Content == nil && ReasoningContent == "" && hasReasoningBlocks` → `Content = ""`；注释「Strict OpenAI-style providers reject role-only messages」）
- 当前行为（我们）：`scenario-thinking-signature-wire` 显示我们保留 thinking 块往返；对「仅 reasoning、无可见文本」的回合在 chat 出站的形态未入库。
- 目标行为：不产生「role-only」消息；要么降级空可见内容，要么整块丢弃并留诊断。
- 优先级：P2。
- triage：**部分修**——补样本 `scenario-reasoning-only-turn`（anthropic→chat），先对拍再定。

### 4. thinking→reasoning_effort 阈值口径 [no-change，记录]

- 样本/出处：higress `<4096→low，<16384→medium，≥16384→high`；我们 `≤5000→low，≤15000→medium，>15000→high`（`internal/transformer/inbound/anthropic/thinking.go`）
- 差异：阈值不同（4096/16384 vs 5000/15000），都是启发式。
- triage：**no-change**——不追逐第三方启发式数字；如要统一，以我们出站表为准（或等标准表统一议题，见 CLIProxyAPI 审计 #1）。

### 5. Claude input_tokens 口径（cached 扣除） [no-change，已对齐]

- 样本/出处：`computeClaudeInputTokens`（total==prompt+completion 时判定 OpenAI 语义并扣除 cached）
- 当前行为（我们）：`usage_estimate` 与日志/计费链路已有「真实输入/命中缓存」双列展示（线上日志可见），口径处理已在。
- triage：**no-change**（已对齐；该函数仅作交叉验证证据）。

### 6. 流式 `[DONE]` 兜底关块与重复 usage 忽略 [fix-p2，观察]

- 样本/出处：`ConvertOpenAIStreamResponseToClaude` 中 `[DONE]` 处理与 `messageStopSent` 后的 chunk 忽略
- 当前行为（我们）：直通路径不重建事件流（不需要这层）；转换路径逐事件映射，未见「上游漏 [DONE]/重复 usage」的专门兜底。
- triage：**挂起**——二期流式对拍时补样本（`scenario-stream-no-done`、`scenario-stream-duplicate-usage`）。

## triage 汇总

| # | 主题 | 优先级 | 结论 |
| --- | --- | --- | --- |
| 1 | `cch=` 剥离（缓存） | P1 | **已修**（v2.9.1 fix-p1） |
| 2 | 工具块串行化（流式） | P2 | 挂起（二期流式对拍） |
| 3 | reasoning-only 回合降级 | P2 | 部分修（补样本） |
| 4 | effort 阈值口径 | — | no-change |
| 5 | input_tokens 口径 | — | no-change（已对齐） |
| 6 | `[DONE]`/重复 usage 兜底 | P2 | 挂起（二期流式对拍） |
