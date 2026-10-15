# 审计：new-api（relayconvert）Claude/Gemini 转换与 reasoning 子系统

- 源：`Calcium-Ion/new-api` @ `718a4e7f`（AGPL-3.0；module `github.com/QuantumNous/new-api`），本地只读副本 `_diagnostics/conformance-research/new-api/`
- 审计范围：`relaykit/relayconvert/`（新转换矩阵 + golden tests）、`relay/channel/{claude,gemini}`（存量链路）
- 定位：经验来源（AGPL 与我们相容，可借鉴设计不可直接拷贝代码而不留痕；实际上我们只提炼行为）。

## 结论摘要

1. new-api 的 relayconvert 是**带 golden 测试的完整转换矩阵**（request/response/stream × openai chat / openai responses / claude messages / gemini chat，`testdata/golden/**/*.json` 逐字节留档）——和我们的对拍基建思路相同，区别是它把 golden 当断言基线、我们把它当三方对拍。
2. 它的 reasoning 子系统是目前四源里最完整的：**Intent 抽象**（Mode{enabled/adaptive/disabled} × Effort{none…max} × Source{explicit/native/suffix/pivot}）、冲突检测（返回 4xx ClientError）、两种模型名后缀语法、以及未知 Claude 兼容模型「adaptive→enabled 降级」的安全策略。
3. Claude 出站的**参数联动钳制**非常实用：`budget_tokens` 下限 1024、`budget < max_tokens` 强制成立（必要时抬高 max_tokens），并产出诊断码（`claude_budget_adjusted` / `claude_max_tokens_raised`）。我们目前完全没有这层保护（P1）。
4. 工具结果图片：new-api 与我们计划同路——tool 消息只留文本（空则 `[image]` 占位），图片外提到后续 user 消息；理由注释写得很直白：「Upstreams reject empty tool content」且「stringifying them would hand base64 image data to the upstream text tokenizer」。
5. Gemini 出站：只认 `extra_body.google.thinking_config`（snake_case），对 camelCase 直接**显式报错**（而不是静默忽略）——这是「宁可 400 也不静默」的取向，与我们日志的显式诊断理念一致。

## 差距清单

### 1. Claude thinking 与 max_tokens 的联动钳制 + 诊断码 [fix-p1]

- 样本/出处：`relaykit/relayconvert/claude_default_max_tokens_test.go`（`budget below 1024 is raised`、`budget at max_tokens is lowered`、`max_tokens too small for manual thinking is raised`，诊断码 `claude_budget_adjusted` / `claude_max_tokens_raised`）
- 修复前行为（我们，v2.9.1 之前）：`internal/transformer/outbound/anthropic/messages.go` 的 `getThinkingBudget` 只做 effort→budget 映射（low=1024/medium=8192/high=32768/xhigh=65536/max=128000），不校验 `budget ≥ 1024`、不保证 `budget < max_tokens`。
- 目标行为：出站前钳制（下限 1024；`budget ≥ max_tokens` 时按 new-api 口径降到 `max_tokens-1`，仅当 max_tokens ≤1024 装不下最小预算时抬到 `max(1280, budget+1)`），并留痕。
- 优先级：P1（直接消除一类上游 400）。
- triage：**已修**——与 CLIProxyAPI 审计第 2 条合并为 v2.9.1 fix-p1；样本 `scenario-thinking-budget-vs-max-tokens`（thinking.enabled + budget_tokens=4096 + max_tokens=4096 → 出站 budget=4095）已入库并通过三方对拍；口径最终取 new-api 的「budget=max_tokens-1」，仅 max_tokens ≤1024 时才抬 max_tokens（`claude_max_tokens_raised` 语义）。

### 2. 工具结果图片外提策略 [fix-p0，已并入 v2.8.14]

- 样本/出处：`internal/claude_messages/to_oai_chat_req.go`（`claudeToolResultToChat`，行 ~289–318 注释与实现）
- 当前行为（我们）：丢图（对拍三连证据：`scenario-tool-result-image-base64/-url`）。
- 目标行为：tool 消息保留文本（空则占位），图片放到后续 user 消息的 content 数组（`image_url`），并在文本里标明归属。
- 优先级：P0。
- triage：**修**——v2.8.14 计划内；实现对上游兼容性取「chat/anthropic 直放数组、responses 外提」的二段策略（与 sub2api 审计对齐）。

### 3. developer 角色语义 [fix-p1]

- 样本/出处：`relaykit/relayconvert` 的 Claude messages 转换（system 部分将 `ParseSystem()` 的各块聚合为 system 消息；另有 sub2api 的 developer role 专测）
- 当前行为（我们）：`scenario-system-developer-role` 显示 developer 消息整条丢失。
- 目标行为：不丢；合并进 system 或按目标协议保留 developer。
- 优先级：P1。
- triage：**修**——与 sub2api 审计第 2 条合并。

### 4. reasoning 抽象与模型名修饰符 [fix-p2/挂起]

- 样本/出处：`relaykit/relayconvert/reasoning/{intent,suffix,claude,gemini}.go`
  - Effort 链后缀：`-max/-xhigh/-high/-medium/-low/-minimal`（OpenAI 另允许 `-none`）；
  - `@key:value` 修饰符：`@thinking:on|adaptive|off|<budget>`（非法值 → ClientError 4xx）；
  - `MergeExplicit/MergeExplicitAndSuffix` 冲突检测；`ClaudeDefaultEffort(model)`；`EffortFromBudget` 双向换算；
  - 未知 Claude 兼容模型：adaptive 降级为 enabled 手动形式（「cannot safely assume」）。
- 当前行为（我们）：无后缀/修饰符；effort 由请求体与渠道配置决定；Gemini 家族有自研分类与钳制。
- 目标行为（可选）：请求级覆写 thinking；冲突显式报错。
- 优先级：P2。
- triage：**挂起**（与 CLIProxyAPI 审计第 3 条同议题，等用户需求确认）。

### 5. Claude 缺省 max_tokens 的口径 [no-change，记录]

- 样本/出处：`relaykit/relayconvert/internal/oai_chat/to_claude_messages_req.go`（`DefaultMaxTokensFor(model)` 可配置钩子；缺省未配置时不注入）
- 当前行为（我们）：`resolveMaxTokens` 固定缺省 8192（`internal/transformer/outbound/anthropic/messages.go:363`）。
- 差异：我们总是给缺省值（保证 Claude 上游不因缺 max_tokens 400）；new-api 默认不注入、可按模型配置。
- triage：**no-change**——我们的固定缺省更保守（Claude 的 `max_tokens` 是必填）；是否改为可配置留给后续配置面设计，不在协议层范围。

### 6. Gemini `thinking_config` 命名与显式报错 [fix-p2]

- 样本/出处：`relaykit/relayconvert/internal/oai_chat/to_gemini_chat_req.go`（拒绝 `extra_body.google.thinkingConfig`，报「use thinking_config instead」；认 `thinking_budget/include_thoughts/thinking_level`）
- 当前行为（我们）：出站 Gemini 用 `generationConfig.thinkingConfig.{thinkingLevel,thinkingBudget}`（camelCase，Gemini 官方 REST 形式）。
- 差异：new-api 面向 OpenAI 客户端的 `extra_body.google.*` 通道用 snake_case；两者不冲突（我们不走 extra_body 通道）。
- triage：**no-change**——但记录：若未来支持 `extra_body.google.*` 透传，需要按 new-api 的显式报错思路处理命名混用。

## triage 汇总

| # | 主题 | 优先级 | 结论 |
| --- | --- | --- | --- |
| 1 | thinking×max_tokens 钳制+诊断 | P1 | **已修**（v2.9.1 fix-p1，与 CLIProxyAPI #2 合并） |
| 2 | 工具结果图片外提 | P0 | **修**（v2.8.14） |
| 3 | developer 角色 | P1 | **修**（与 sub2api #2 合并） |
| 4 | reasoning 修饰符语法 | P2 | 挂起（等需求确认） |
| 5 | Claude 缺省 max_tokens | — | no-change（记录） |
| 6 | Gemini thinking 命名/报错 | — | no-change（记录） |
