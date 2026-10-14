# 审计：CLIProxyAPI 转换矩阵与 thinking 映射（协议层三段式 v2.8.15）

- 源：`router-for-me/CLIProxyAPI` @ `5fc580b1`（v8.0.29，MIT），本地只读副本 `_diagnostics/routing-research/repos/CLIProxyAPI/`
- 审计范围：`sdk/translator`（对拍所用的官方 SDK 入口）、`internal/translator/*/init.go`（注册矩阵）、`internal/thinking/*`（thinking 归一化）
- 角色：我们的**次裁判**（纯函数、无网络、无认证；`conformance/oracle-cliproxyapi` 以独立 module 引用）

## 结论摘要

1. 矩阵是「注册制」：`Registry.requests[from][to]` / `Registry.responses[from][to]` 两张表，无注册即**回退原报文**（只归一 model 字段）——对拍时用 `HasRequestTransformer` 区分 `unsupported`（我们的 deviations.json 4 条 unsupported 即由此而来）。
2. 同协议对：OpenAI→OpenAI 与 Gemini→Gemini 有注册（Gemini 的用于归一化非法 role）；**Claude→Claude 没有**（其设计选择：同协议不再序列化）。
3. `X→OpenaiResponse` 仅 `Interactions→OpenaiResponse` 有请求侧转换器；`openai/openai/responses` 是反方向（OpenaiResponse→OpenAI chat）；`claude/openai/responses` 只有响应侧转换（请求侧 nil，配合别的路径使用）。
4. thinking 子系统明显比我们现有实现更「**集中**」：所有 provider 的 thinking 配置抽取/校验/钳制走同一条流水线（`ApplyThinking`），并引入 **模型名后缀** 语法与 **每模型能力表**（levels/min/max）。
5. 值得吸收的：thinking level↔budget 表（比我们 anthropic 出站的固定表更细）、每模型钳制、后缀语法（可选）、`includeThoughts` 的显式化。
6. 明确不吸收：把未知模型一律「当用户自定义模型、交给上游校验」（我们没有 per-model 能力表，贸然透传会把 400 留给用户）；对 Claude `system`/`tool_calls` 的表示取舍（差异见 deviations.json 对应条目）。

## 差距清单

### 1. thinking level↔budget 的标准表与阈值化换算 [fix-p2]

- 样本/出处：`internal/thinking/convert.go`
- 源行为：
  - `levelToBudgetMap`：`none→0`、`auto→-1`、`minimal→512`、`low→1024`、`medium→8192`、`high→24576`、`xhigh→32768`、`max→128000`；
  - `ConvertBudgetToLevel` 用阈值分段回映（1–512→minimal，513–1024→low，1025–8192→medium，8193–24576→high，>24576→xhigh）；
  - `MapToClaudeEffort`：`minimal→low`，`low/medium/high` 原样，`xhigh/max→max(不支持则high)`，`auto→high`。
- 当前行为（我们）：`internal/transformer/outbound/anthropic/messages.go:getThinkingBudget` 固定表（low=1024/medium=8192/high=32768/xhigh=65536/max=128000）；Gemini 出站有独立家族表（`outbound/gemini/budget.go`，含逐家族钳制）。
- 目标行为：把「effort↔budget」收敛为一张共享表（或以我们的家族表为准，把 anthropic/其他出站对齐），避免各出站各写一套导致同一请求跨出站换算不一致。
- 优先级：P2（无已知线上缺陷；统一化收益是长期一致性）。
- triage：**挂起**——等协议层第二阶段做「出站换算统一」时一并处理，届时用对拍样本回归。

### 2. thinking budget 与 max_tokens 的联动钳制 [fix-p1]

- 样本/出处：`internal/thinking/validate.go`（校验/钳制）、`kimi_max_clamp_repro_test.go`；对照 new-api 的 `claude_budget_adjusted` / `claude_max_tokens_raised` 诊断（见 `newapi-claude-gemini.md`）
- 源行为：按目标模型能力（`ModelInfo.Thinking` 的 levels/min/max）校验并钳制 budget/level；不支持 thinking 的模型直接剥离配置；未知模型透传给上游校验。
- 当前行为（我们）：anthropic 出站 `getThinkingBudget` 只做 effort→budget 映射，**不检查 `budget_tokens < max_tokens` 也不检查 `≥1024`**；用户传 `thinking.budget_tokens ≥ max_tokens` 时会把 400 直接透传。
- 目标行为：出站前置钳制——`budget` 下限 1024、`budget ≤ max_tokens-1`（必要时抬高 max_tokens），并在聊天日志诊断字段留痕（对齐 new-api 的诊断码思路）。
- 优先级：P1（真实 400 类缺陷，且我们已有 `thinking.budget_tokens` 与 `max_tokens` 相关的历史反馈）。
- triage：**修**——协议层第二阶段第一个小 PR 候选；样本先行（新增 `scenario-thinking-budget-vs-max-tokens`），修 `outbound/anthropic` 一处 + 单测。

### 3. 模型名后缀语法（请求级覆写 thinking） [fix-p2]

- 样本/出处：`internal/thinking/suffix.go`（`model(value)`，如 `claude-sonnet-4-5(16384)`、`gpt-5.2(high)`、`...(...none/auto/-1)`）；new-api 另有 `-high` 链与 `@thinking:on` 修饰符（见其审计）
- 源行为：后缀优先于请求体配置；解析优先级 special(none/auto/-1) → level → numeric budget。
- 当前行为（我们）：无后缀语法；thinking 只由请求体字段与分组配置决定。
- 目标行为：（可选）支持 `model(effort|budget)` 覆写，便于用户不改客户端配置做 A/B。
- 优先级：P2（体验增强，非缺陷；两个参考实现都做了，说明有真实需求）。
- triage：**挂起**——等用户确认是否需要；实现成本中等（inbound 解析 + 出站透传链路都要认这个语法）。

### 4. per-model thinking 能力表 [暂不做]

- 样本/出处：`internal/registry`（`ModelInfo.Thinking{Levels, Min, Max}`）与 `detectModelCapability`（budget-only / level-only / hybrid）
- 源行为：注册表里逐模型声明能力；未知模型 = 用户自定义 → 不校验、直接透传。
- 当前行为（我们）：无 per-model 能力表；Gemini 家族按模型名前缀分类（`classifyGeminiFamily`），anthropic/其他靠请求透传。
- triage：**不做（本轮）**——这实质是「渠道×模型能力学习」的一部分，已立项在路由会话/后续阶段；协议层不重复造表，等能力表落地后把钳制挂上去。

### 5. 对拍矩阵缺口（登记簿已记录） [no-change]

- `baseline-anthropic-to-anthropic` / `-to-responses`、`baseline-chat-to-responses`、`baseline-responses-to-responses` → CLIProxyAPI `unsupported`（无 Claude→Claude、无 X→OpenaiResponse）。
- 说明：这不是缺陷，是其矩阵设计；登记为 `unsupported` 只为了 CI 语义完整（其余 16 个样本走三方 diff）。

## triage 汇总

| # | 主题 | 优先级 | 结论 |
| --- | --- | --- | --- |
| 1 | level↔budget 表统一 | P2 | 挂起（与出站换算统一一起做） |
| 2 | budget 与 max_tokens 联动钳制 | P1 | **修**（第二阶段首个小 PR 候选） |
| 3 | 模型名后缀语法 | P2 | 挂起（等用户确认） |
| 4 | per-model 能力表 | — | 不做（归渠道能力学习立项） |
| 5 | 矩阵缺口 | — | no-change（登记为 unsupported） |
