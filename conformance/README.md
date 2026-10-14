# 协议层三方对拍（conformance）

本目录是 **Octopus 协议层三段式（v2.8.15 起）** 的对拍基建：同一份请求样本
分别穿过三个独立实现，比较各自产出的上游请求体（三方差分）：

| 角色 | 实现 | 形态 | 许可 |
| --- | --- | --- | --- |
| 被测方（ours） | 本仓库 `internal/transformer`（入站解析 + 出站序列化） | 测试内直接调用 | AGPL-3.0 |
| 主裁判 | [looplj/axonhub](https://github.com/looplj/axonhub) `llm` 模块 | 独立 Go module，测试期库依赖 | Apache-2.0 |
| 次裁判 | [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) `sdk/translator` | 独立 Go module，官方 SDK 入口 | MIT |

两位裁判都是**纯转换库**：不联网、不需要凭据（只使用占位 base URL 与 key），
因此可以在 CI 里离线对拍。

## 目录结构

```
conformance/
  README.md                 本文件
  deviations.json           偏差登记簿（未登记差异 = CI 红）
  samples/*.json            黄金样本（request 方向一期执行；response 方向二期）
  oracle-axonhub/           主裁判 CLI（stdin/stdout JSON）
  oracle-cliproxyapi/       次裁判 CLI（stdin/stdout JSON）
internal/conformance/       harness（纯 *_test.go + 一个占位 doc.go）
docs/conformance/audit/     四源经验审计笔记（new-api / higress / CLIProxyAPI / sub2api）
```

## 样本词汇表

- 入站：`openai_chat` / `openai_responses` / `anthropic`（本期），`gemini`（保留）
- 出站：`chat` / `responses` / `anthropic` / `gemini`
- 一期矩阵：入站 {openai_chat, openai_responses, anthropic} × 出站 {chat,
  responses, anthropic, gemini}，每格至少一个 baseline 样本；场景样本覆盖
  8 类真实缺陷（工具结果图片、尾随 reasoning、多 assistant 分隔、system/developer
  角色、多轮 reasoning、文本形式工具调用、thinking signature 线形状、并行工具配对）。

样本字段：`id`（= 文件名）、`class`、`side`（request/response）、`inbound`、
`outbound`、`model`、`stream`、`source`（出处：issue / test / baseline）、
`request`（原始入站请求体）。`side=response` 的样本标注 `phase: 2`，一期只登记
不执行（响应方向的规范化对照在第二阶段启用）。

## 运行方式

CI：`conformance-oracle` job 先构建两个 oracle 二进制，再运行

```
go test -count=1 -v ./internal/conformance/...
```

环境变量：

- `CONFORMANCE_ORACLE_AXONHUB`：主裁判二进制路径（缺省则跳过该裁判）
- `CONFORMANCE_ORACLE_CLIPROXYAPI`：次裁判二进制路径（缺省则跳过）
- `CONFORMANCE_REPORT`：完整对照报告输出路径（CI 作为 artifact 上传）

常规 `build-and-test` job 也会运行本测试，但**不提供 oracle**：此时只复现我方
当前行为并写日志，不会因未登记差异失败。

> Windows 本机提示：该测试会拉起含 tiktoken 词表的转换链，历史上曾在 Windows
> 触发杀软误报（详见 AGENTS.md）。本机只做 `go build` / `go vet`，执行一律在
> Linux CI。

## 差异判定与登记簿

- 双方都成功：对规范化 JSON（键排序、数字按值比较）做深度 diff，路径为 JSON
  Pointer 风格；同时比较 `METHOD path` 端点。任何差异都需要在
  `deviations.json` 登记（`outcome=diff` + 精确 `diff_paths`），否则 CI 红。
- 一方失败：按 `ours_error_oracle_ok` / `ours_ok_oracle_error` / `both_error`
  登记（错误文案不比较）。
- oracle 缺该方向转换器：登记 `outcome=unsupported`（当前 4 条：CLIProxyAPI 无
  responses 出站、无 Claude→Claude）。
- `outcome=pending`：首次校准未完成时的临时状态（CI 会告警），应尽快用
  artifact 报告校准成 `diff`。

校准流程：CI 跑红 → 下载 artifact `conformance-report.json` → 用其中
`diff[].path|kind` 生成登记条目 → 重推分支 → 绿。

## 与路由会话（01a13ac8）的协作边界

本目录（`conformance/`、`internal/conformance/`、`docs/conformance/`）为**纯新增
目录**，与路由/调度改动零冲突。第二阶段「逐条修复」开始触碰 `internal/transformer`
与 `internal/relay` 时遵循：

1. `internal/relay/relay.go` 主干归路由会话，协议修复一律「独立文件 + 单点接线」
   （v2.8.13 `usage_estimate.go` 模式）；
2. 每个 PR 前 rebase `ci/master`，小步合并；
3. i18n 文案只追加不重排；
4. 协议层只依赖既有 `Inbound`/`Outbound` 接口；路由侧若需改 relay 主干签名，
   先在对拍会话同步。

## 许可与出处

- CLIProxyAPI（MIT）：以 Go module 依赖方式引入（`sdk/translator` +
  `sdk/translator/builtin` 官方 SDK 入口），保留其版权与许可声明。
- AxonHub（Apache-2.0）：以 Go module 依赖方式引入（`llm/transformer/...`），
  保留 NOTICE/版权声明。
- 两个 oracle 均为**测试期依赖**，不进入生产构建。
