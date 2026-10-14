# 审计：sub2api apicompat（Responses/Anthropic/Chat 深保真转换）

- 源：`Wei-Shaw/sub2api` @ `9c2cca9e`（LGPL-3.0），本地只读副本 `_diagnostics/conformance-research/sub2api/`
- 审计范围：`backend/internal/pkg/apicompat/`（约 350KB 源码 + 450KB 测试；本目录即其协议转换核心）
- 定位：**不是裁判**（无 SDK 包装、服务部署重、ent ORM）——审计 + 案例库来源；本笔记的每条差距都优先转化为我们的对拍样本。

## 结论摘要

1. 这个包的测试是最有价值的资产：几乎每条真实缺陷都先固化成用例（`responses_to_anthropic_tool_pairing_test.go`、`chatcompletions_responses_tool_output_media_test.go` 等），可直接提炼成我们的 conformance 样本。
2. 三个「我们已知会踩」的点在这里都有明确答案：
   - thinking 块必须**始终带 `signature` 键**（空串也算）——严格客户端会报 `missing field signature`；
   - **developer 角色必须保留**（降级成 user 会悄悄改变指令等级）；
   - 工具输出里的图片要**搬出 tool output** 到后续 user 消息（否则原生 Responses 上游报 `No tool output found for tool call`）。
3. 流式侧有两套值得学的工程手段：终端事件文本恢复（上游不发 delta、只在 done/terminal 里给全文时，补发缺失文本且不重复）与并行工具参数打包（多个 tool call 的 arguments 在 done 前收齐再逐块发出，避免「幽灵增量」）。
4. 与我们现状最直接的冲突面：我们的 v2.8.14 计划（工具结果 URL 图片）与这里的「媒体外提」策略同源；thinking signature 与我们 `scenario-thinking-signature-wire` 的 P1 修复方向一致。

## 差距清单

### 1. Anthropic thinking 块始终带 signature 键 [fix-p1，与 axonhub 对拍同结论]

- 样本/出处：`thinking_signature_wire_test.go`（`TestAnthropicContentBlock_ThinkingAlwaysHasSignature`）——注释原文：「strict clients such as Grok Build fail with missing field `signature`」
- 当前行为（我们）：`scenario-thinking-signature-wire` 对拍显示——客户端发来的 thinking 块缺 signature 时，我们原样透传（缺键）；axonhub 补 `"signature": ""`。
- 目标行为：序列化 thinking 块时保证 `signature` 键存在（缺省空串）；真实 signature（上游返回的）原样保留。
- 优先级：P1（会导致严格客户端/上游 400 的线形状缺陷）。
- triage：**修**——与 axonhub 条目的 P1 合并为同一个修复：出站 anthropic（以及任何会重建 thinking 块的路径）统一「signature 键必在」。样本已入库。

### 2. developer 角色在跨协议转换中保留 [fix-p1]

- 样本/出处：`chatcompletions_to_responses_developer_role_test.go`（「Converting it to \"user\" silently demotes the caller's instructions to user turns」）、`responses_to_anthropic_instructions_test.go`（`TestConvertResponsesInputToAnthropic_DeveloperRole`）
- 当前行为（我们）：`scenario-system-developer-role` 对拍显示——developer 消息被我们**整条丢弃**（anthropic 出站只剩 1 块 system）；两个裁判都保留了它（axonhub 并入 system 块数组，CLIProxyAPI 同）。
- 目标行为：developer 语义不丢——出站 anthropic 合并进 `system` 块（保持顺序：system → developer），出站 chat 可保留 `role: "developer"` 或降级为 system，但绝不能丢内容。
- 优先级：P1（静默丢指令，行为影响大且难自查）。
- triage：**修**——第二阶段小 PR：inbound 保留 developer role（若未保留）+ anthropic 出站合并策略 + 单测；样本已入库。

### 3. 工具输出媒体外提（Responses 语义） [fix-p0，已并入 v2.8.14 计划]

- 样本/出处：`responses_tool_output_media.go`（`LiftResponsesToolOutputMedia`）与 `chatcompletions_responses_tool_output_media_test.go`（8 个用例覆盖并行批次、孤儿调用、重复 call_id、富兄弟内容保留等）
- 源行为：把 `function_call_output.output` 里的 image 部分搬出，tool output 只留文本（或空），图片以 `input_image` 形式放进**紧随其后的 user 消息**（带 `Tool output media (call_id ...)` 归属文本）；注释说明上游（DeepSeek 原生 Responses）只接受字符串 output，图片留在里面会报「No tool output found for tool call」；批处理时保持并行 tool 输出连续（developer/system 通知延后到批次结束）。
- 当前行为（我们）：`scenario-tool-result-image-base64/-url` 对拍显示——anthropic 入站工具结果里的图片被我们**直接丢弃**（tool content 为空串）；axonhub 保留图片在 tool content 数组，CLIProxyAPI/new-api 则外提到后续 user 消息。
- 目标行为：至少「不丢图」；方向二选一按上游兼容性定（见候选方案）。
- 优先级：P0（内容丢失；用户已有「多图卡住」历史）。
- triage：**修**——v2.8.14 计划已含「Anthropic 工具结果 URL 图片修复」；本审计补充两个决策点：①tool 消息 content 直接放 image 数组（axonhub 式）对 OpenAI 兼容上游兼容性最好；②若目标上游是原生 Responses（Codex/DeepSeek），必须用 sub2api 式外提。建议：出站 chat/anthropic 走直放，出站 responses 走外提。

### 4. 流式文本恢复（终端事件兜底） [fix-p1]

- 样本/出处：`responses_to_anthropic_text_recovery_test.go`（20+ 用例：从 done/terminal 恢复文本、按双 index 去重、只在缺失时补后缀、不重复已发 delta、tool-only 回合不发文本）
- 源行为：上游不发文本 delta、只在 `response.completed` 等终端事件里给全文时，转换器**补发**缺失文本（并精确去重）；这就是「假流式」下游的表现层对策。
- 当前行为（我们）：流式转换按上游事件直通（我们自己的直通链路保留原始 SSE；转换链路逐事件映射），没有「终端全文恢复」逻辑——若上游只在 done 给全文，客户端会看到空流（对应我们线上观察到的「假流式」渠道：正文在结束时集中到达）。注意：我们的问题是上游**真的**在最后一次性发正文（内容没丢，只是晚），与 sub2api 面对的场景（上游完全不给 delta）不同。
- 目标行为：先不改——我们的直通链路已把「晚到」如实透传；是否需要「恢复」取决于是否有上游只给 done 不给全文。留一个样本观察。
- 优先级：P1（可能影响沉浸式翻译等长流客户端；但结论取决于上游形态）。
- triage：**挂起**——待有真实「只给 done」的日志样本后再定；已在案例库留 `scenario-terminal-only-text`（待补样本）。

### 5. 并行工具调用配对与孤儿清理 [fix-p2]

- 样本/出处：`responses_to_anthropic_tool_pairing_test.go`（orphan tool_result 丢弃、dangling call 丢弃、并行一答一缺）、`responses_to_anthropic_parallel_tool_test.go`（三并行工具流式打包）
- 源行为：入站清洗阶段就修复配对——孤儿 tool_result 删除、悬空 tool_use 删除；流式输出时并行工具参数先缓存、在 done 前成块发出，避免「幽灵 delta」。
- 当前行为（我们）：`scenario-parallel-tool-pairing` 对拍显示我们的配对行为与 axonhub 逐字段一致（干净）；孤儿/悬空场景未入库。
- 目标行为：保持现有配对逻辑；补孤儿/悬空两类样本防回归。
- 优先级：P2（现状良好，补样本为主）。
- triage：**部分修**——新增两个样本（孤儿 tool_result / 悬空 tool_use），先对拍登记；若出现差异再修。

### 6. reasoning item 与无效块清洗 [no-change，观察]

- 样本/出处：`responses_to_anthropic_invalid_blocks_test.go`（reasoning item 带内容/仅 summary 均丢弃；未知 item 类型保文本、丢结构；仅含未知 part 的消息整条丢弃；`InputFile` data URI → document）
- 当前行为（我们）：`scenario-trailing-reasoning-item` 显示我们对 Gemini 出站丢弃 thought part（P0，另条）；对未知块我们没有统一清洗层。
- triage：**不做（本轮）**——我们的 inbound 是按协议严格解析，未知块的处理策略应等「宽松恢复」立项（与 sub2api 的 invalid-block recovery 一起设计），不在 v2.8.15 范围内。

## triage 汇总

| # | 主题 | 优先级 | 结论 |
| --- | --- | --- | --- |
| 1 | thinking signature 键必在 | P1 | **修**（与 axonhub 条目合并） |
| 2 | developer 角色保留 | P1 | **修** |
| 3 | 工具输出媒体外提 | P0 | **修**（并入 v2.8.14） |
| 4 | 流式文本终端恢复 | P1 | 挂起（待真实样本） |
| 5 | 并行工具配对/孤儿清理 | P2 | 部分修（补 2 个样本） |
| 6 | 无效块宽松恢复 | — | 不做（另立项） |
