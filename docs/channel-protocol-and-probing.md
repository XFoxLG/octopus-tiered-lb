# 渠道级协议下沉与能力探测

本文记录「把出站协议决策从分组下沉到渠道」这一轮改动的设计依据、实施边界与验收条件。
面向后续维护者：如果只读一段，先读下面的「为什么改」和「三个不变量」。

## 为什么改

出站协议原本是**分组**属性（`groups.outbound_format`），但同一个分组里常常同时存在：

- 只支持 `POST /v1/chat/completions` 的公益站；
- 原生 Anthropic 渠道；
- 原生 Gemini 渠道；
- 同时支持多协议、但每个协议在不同路径上的中转站（例如火山方舟的 OpenAI 兼容在
  `/api/v3`、Anthropic 兼容在 `/api/compatible`）。

协议是**渠道**的属性，不是分组的属性。把它放在分组层有两个直接后果。

### 后果一：透传会主动打坏原生渠道（缺陷）

`passthrough` / `raw` 是"请求体原样转发、只改写 `model` 字段"的模式，它只认三种入站
格式（Chat Completions / Responses / Anthropic Messages），见
`internal/transformer/outbound/passthrough/passthrough.go` 的
`delegateAndEndpointForFormat`。

改前的 `resolveAttemptTypesByFormat` 在判断 `passthrough` / `raw` 时**完全没有看渠道类型**，
只要入站格式属于那三种就返回透传适配器。于是分组一旦设成 `passthrough`，
分组里的 Gemini 渠道会被塞进透传器：请求体是 OpenAI 形状，却被发到 Gemini 的原生端点。
这不只是"不方便"——它会用一个必然失败的畸形请求去打上游，并被上游计入渠道健康度。

修复方式：新增 `SupportsPassthroughFormat` 闸门，只有 OpenAI 兼容渠道
（Chat / Response / MiMo）能承载透传，其余渠道回落到自己的原生适配器。
Cloudflare 虽然入站是 OpenAI 风格，但端点路径由 model 名拼成
（`/ai/run/@cf/{model}`），透传的固定端点路径不成立，同样排除。

### 后果二：多协议渠道无法表达自己的地址映射

渠道的每条 `BaseUrl` 本来就带 `suffix_mode`，而它的合法取值里包含
`anthropic` / `gemini` / `volcengine`——协议信息其实**有地方放**，只是：

1. 前端只开放了「OpenAI 兼容 / 自定义」两个选项，其余值写不进去；
2. relay 每次只取"延迟最低的那一条"（`GetNormalizedBaseUrl`），完全不看它是给哪个
   协议用的。

结果是这类站点只能靠"只填一条地址 + 不切协议"来绕过，一旦启用协议回退就会打到错的路径。

## 三个不变量

改动被这三条约束框住，任何一条被破坏都视为回归：

### 不变量 1：渠道未声明协议时，行为与改动前逐字节一致

`channels.upstream_protocols` 为空（NULL / 空数组 / 全空串）时，
`ResolveAttemptTypesForChannelDeclared` 直接委托给改动前的
`ResolveAttemptTypesForChannel`，即"旧渠道 override > 分组 outbound_format"。

这条不是口头承诺，而是被穷举断言固化的：
`internal/relay/channel_protocol_compat_test.go` 遍历
「3 种入站格式 × 7 种渠道类型 × 10 种分组格式 × 4 种旧覆盖值」的全部组合，
断言新解析函数与旧函数返回完全相同的序列。

### 不变量 2：超时字段的零值必须安全

渠道级超时（`first_token_time_out` / `attempt_time_out` / `stream_idle_timeout`）的语义是：

| 值 | 含义 |
|----|------|
| `0` | 跟随分组（**默认值**） |
| `-1` | 显式关闭该看门狗 |
| `>0` | 秒数 |

这里刻意让「未覆盖」等于 Go 零值，而不是用 `-1` 之类的外部哨兵。
原因：渠道对象既从数据库反序列化，也在内存里临时构造（探测、缓存重建、测试）。
哨兵方案会让任何一处忘了显式赋值的构造点**静默改变转发行为**——一个
`&Channel{}` 会被解释成"用户显式关闭了看门狗"。

同样的教训在 `CircuitBreakerThreshold` 上已经吃过一次（该字段的注释原文：
「内存构造的结构体零值即"跟随全局"，无 -1 哨兵的零值陷阱」）。

取值链实现的唯一入口是 `model.ResolveTimeoutOverride`，测试覆盖全部五个分支。

### 不变量 3：探测与真实转发使用同一套解析规则

渠道探测（分组模型测试、分组健康、渠道编辑页单模型同步、tools 探测）如果和主转发
路径用不同的协议解析或不同的地址选择，就会出现"探测失败但转发正常"的假阴性，
反过来也会让用户对探测结果失去信任。

因此四处探测路径与 `relay.go` 全部调用同一个
`outbound.ResolveAttemptTypesForChannelDeclared`，地址则统一通过
`outbound.EndpointProtocolForAdapter(adapterType)` → `GetNormalizedBaseUrlForProtocol`
选取——即"这次真的要打哪个协议，就用那条对应地址"。

## 数据模型

### 新增列（迁移 068）

| 列 | 类型 | 语义 |
|----|------|------|
| `channels.upstream_protocols` | text（JSON 数组） | 渠道声明的协议有序列表；空 = 沿用分组 |
| `channels.first_token_time_out` | int，默认 0 | 渠道级首字超时覆盖 |
| `channels.attempt_time_out` | int，默认 0 | 渠道级单次尝试超时覆盖 |
| `channels.stream_idle_timeout` | int，默认 0 | 渠道级流式空闲超时覆盖 |
| `channels.reasoning_buffer_strategy` | varchar(20)，默认 '' | 渠道级缓冲策略；空 = 沿用分组 |

`BaseUrl` 增加 `protocol` 字段（JSON，非数据库列）：声明这条地址服务于哪种协议。

### 协议词汇

八个取值与历史 `outbound_format` 保持同一套词汇，便于旧值平移：
`chat` / `responses` / `messages` / `chat_only` / `responses_only` / `messages_only`
/ `passthrough` / `raw`。

`NormalizeUpstreamProtocols` 的规则：

- 逐项小写去空白；未知取值直接报错（不静默丢弃——静默丢弃会让用户在界面看到
  "设置了"、运行时却不生效）；
- 去重；
- **保留用户声明的先后顺序**，顺序即尝试优先级（排序会把这个信息抹掉）；
- 互斥校验：`passthrough` / `raw` 不能与任何其它协议共存；
  `chat_only` 不能与 `chat` 共存（`responses_only` / `messages_only` 同理）。

前端 `upstreamProtocolConflict` 与后端保持同一套互斥规则，并且把互斥项直接置灰，
避免用户点一个必然被拒绝的组合再看到报错。

### 顺序如何展开成 adapter 序列

`ResolveAttemptTypesForChannelDeclared` 对 OpenAI 兼容渠道：

- 按声明顺序收集每个协议的首选项，拼接并去重；
- 于是「勾 chat」→ 只试 Chat，「勾 chat + responses」→ 先 Chat 再 Responses，
  「勾 responses + chat」→ 先 Responses 再 Chat；
- 声明里出现 `passthrough` / `raw` → 短路返回单个透传 adapter（仍受渠道类型闸门约束）；
- 原生协议渠道（Gemini / Anthropic / 火山 / Cloudflare / Embedding）没有"协议回退"
  这个概念，直接返回自己的原生适配器。

## 旧字段的去留

`groups.outbound_format` 与 `channels.outbound_format_override` 在这一轮**保留**：

- 迁移把 `outbound_format_override` 的 `chat_only` / `responses_only` 平移进
  `upstream_protocols`（只写新列为空的行，因此幂等且不覆盖用户已设的新值）；
- 旧列原样保留，后端继续读取（解析优先级里排在渠道新字段之后、分组格式之前）；
- 前端渠道侧不再渲染旧下拉，改为新的协议多选。

删除旧列需要另开一轮，并且要先确认没有外部消费方（备份导出、脚本、文档）依赖它们。
参见 `docs/` 内的后续计划；本轮不做删除是因为"先平移、再确认、最后删"比
"一次删干净"的可回滚性高得多。

## 验收清单

后端：

```bash
go build -buildvcs=false ./...
go test -buildvcs=false ./... -count=1
```

需要保持通过的既有红线（改动不应触碰）：

- 已向下游写出内容后禁止重试；
- `context_length_exceeded` / `content_filter` / `prompt is too long` 等确定性失败
  原样透出状态码与关键字段（下游客户端据此触发自动压缩）；
- `{"error":null}`、usage-only 流帧、`{"choices":[]}` 空响应的既有行为。

前端（Windows 下 `pnpm check` 的 build 步骤是 POSIX 语法，必须分步执行）：

```bash
pnpm lint
pnpm test:i18n
pnpm test:unit
pnpm test:integration-ui
pnpm test:airoute
# PowerShell:
$env:NEXT_PUBLIC_APP_VERSION = "check"; pnpm build
```

## 已知边界

- 本地无法复现公益站/私有网关的非标准报错文案，错误分级的关键词表需要按线上反馈迭代；
- 协议声明是**人工声明**。运行时不会自动学习"哪个协议其实通"——按用户决定，
  不做自动写入，也不做后台定时探测（多数公益站禁止测活，且有测活关键词拦截）。
  探测是手动的，结果需要人工确认后应用。
- 多实例部署下 `upstream_protocols` 走数据库，天然一致；探测结果的落库方案见第二轮。

## 第二轮规划（能力探测）

尚未实施，设计要点已定：

- 手动一键探测，单弹窗内先跑协议层（Chat / Messages / Responses / Gemini 各一行
  「正在通过 / 已通过 / 失败 / 不支持」），再跑能力层（模型列表、文本生成、
  工具调用、结构化输出、联网搜索）；
- 三态判定 `pass` / `fail` / `unsupported`；限流、超时、5xx 归为 unknown 且不写入，
  避免上游抽风把可用协议删掉；
- 落库保留历史；「应用」按钮把通过的协议加进勾选（**只加不减**）；
- 复用渠道的 `SkipModelTest` 开关：开启时默认拒绝探测，需显式勾选才跑；
- 所有回显片段经脱敏，绝不回显 Key。
