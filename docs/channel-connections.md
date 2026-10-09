# 渠道接口配置（version 1）

本轮把连接行为收敛为接口协议与地址。品牌模板和总渠道类型不再出现在新建表单。根据 2026-10-07 的追加决定，新建接口也不提供 Cloudflare 原生选项；经公益站转换的服务选择实际公开的 Chat 等协议。旧 Cloudflare 原生记录保留内部兼容，MiMo 作为 Chat 的可选兼容配置继续观察。

## 配置与执行

`channels.connection_config` 是可空 JSON。为空时完整沿用旧解析器；非空时它是对话与 Embeddings 的连接配置来源。版本、接口 ID、协议、URL、鉴权和目录在写入时校验。

```json
{
  "version": 1,
  "selection": "same_protocol",
  "endpoints": [
    {"id":"chat-main","protocol":"chat","url":"https://example.com/v1","url_mode":"base","auth":"default"},
    {"id":"messages-main","protocol":"messages","url":"https://example.com/anthropic/v1","url_mode":"base","auth":"default"}
  ],
  "catalog":{"endpoint_id":"chat-main","format":"openai"}
}
```

- 同协议优先采用稳定排序，其余接口按配置顺序保留。`configured` 严格保留列表顺序，主要用于等价迁移。
- 每条接口有稳定 ID，同 URL 可用于不同协议或不同鉴权配置。调用链直接传递接口对象，不按 URL 再查后缀模式。
- 所有接口共享渠道身份、Key 池、容量限制和代理。每个实际 HTTP 尝试仍受原有总重试预算约束。
- 协议回退沿用已有错误分类：仅换候选类失败可以尝试下一个接口；客户端确定性错误、已输出内容和断连不能触发整段重放。
- 标准 Chat 保留 `developer` 等已解析消息字段；这不是原始透传，也不保证转换后的所有供应商扩展无损。
- `mimo`、`legacy_mimo` 保留现有 MiMo 适配行为。普通第三方 Chat 不自动启用 MiMo 字段或额外凭据头。
- 转发、分组探测、渠道测试、能力探测及模型同步共同使用连接计划与请求构造器。HTTP 元数据记录 `endpoint_id`；普通日志列表不增加诊断列。

## 地址、鉴权、目录

根地址只在无路径时补 `/v1`（Gemini 为 `/v1beta`），已有路径完整保留，再附加该协议端点。完整地址不补版本或端点路径。查询参数和已编码路径保留；Gemini 流式请求按协议设置操作名与 `alt=sse`。

新 Chat 默认只发送 Bearer；Messages 默认 `x-api-key`，Gemini 默认 `x-goog-api-key`。高级鉴权可显式覆盖，也可不发送 Key。接口请求头最后覆盖渠道级请求头；不向未知服务同时复制多种 Key 头。旧兼容配置保持原适配器鉴权。

“不发送 Key”只控制出站鉴权头；本轮仍沿用现有渠道 Key 池调度，尚不支持完全没有 Key 记录的渠道。

模型目录独立声明格式与来源接口，支持 OpenAI、Anthropic、Gemini 和已有 Cloudflare 目录。完整生成地址要求显式填写目录 URL。手工模式不会发目录请求，也不会以空结果清除模型。分页有上限、重复游标检测和响应大小限制；错误包或 `data:null` 不能伪装为空目录。

透传只允许 Chat、Responses、Messages，且只用于相同入站协议。原始透传继续保留客户端路径，因此静态地址预览不能代表每个客户端自定义路径。没有自动探测、AI 协议猜测或自动学习写回。

## 迁移与回退

072 迁移增加连接 JSON 和探测结果的接口 ID，不删除旧字段。自动迁移仅处理单地址、非自定义后缀、无协议绑定且可证明等价的记录。新旧构造器对入站格式、分组策略、流式/非流式、普通及带命名空间的模型名比较尝试顺序、请求地址、方法、头和正文；不发网络请求。

自动迁移还跳过关联通配或非对话分组的渠道，避免改变旧媒体转发。Gemini 旧适配器对含斜杠模型名的路径处理不同，当前保留人工确认。多地址选择、分组继承、透传、特殊适配器等歧义配置继续使用旧解析器。旧 Gemini CLI OAuth 凭据仍走旧渠道；新 Gemini API 接口明确拒绝把这种凭据当普通 API Key 发送。

编辑页显示旧地址、模式及使用渠道的分组规则。点击配置按钮只改草稿，保存后才启用。无法提供等价草稿的记录从空接口开始，需明确填写。媒体接口尚不在新配置范围；相关渠道应保留旧模式，或先拆分媒体用途再迁移。

已迁移渠道通过旧 API 修改 `type/base_urls/upstream_protocols/outbound_format_override` 会得到 409，普通字段仍可更新。旧探测“应用协议”同样拒绝覆盖新版接口。新版探测逐接口保存诊断证据，不能把某一接口的能力结论写成整个渠道的无条件能力。

回退可使用保留的旧字段，但必须先备份现有连接 JSON并停用该渠道，再由管理员清空连接 JSON、刷新缓存/重启后验证旧行为。旧字段是迁移前快照，不代表后续接口编辑的等价配置。只有新配置的新渠道不能直接降级给旧程序。当前没有一键回退按钮。

## 验证与边界

新增测试覆盖 URL/编码/查询参数、接口顺序与同 URL 身份、鉴权、标准 Chat 角色、透传协议边界、迁移对照、真实旧 schema、迁移幂等和旧字段保留、更新序列化与缓存、旧 API 冲突、目录异常/分页、探测脱敏、Embedding 响应，以及三语言界面与接口编辑交互。

本地测试使用固定 HTTP 夹具，不以一次普通文本成功推断工具、思考、联网或结构化输出全部兼容。用户渠道的私有二改版本、错误正文和实际能力仍需在实验站手动验收。模板精简主要降低配置和维护负担；没有宣称吞吐或资源消耗已有显著提升。

### 2026-10-08 本地验收记录

- Go 构建及相关 relay、helper、model、channel、迁移、handler、middleware、适配器和 task 回归通过；另验证生产 `jsoniter` 标签下的连接相关测试。没有以这些结果声称执行过全仓库 `go test ./...`。
- 前端 lint、三语言键检查、261 项单测、45 项 UI 集成测试、10 项 AI 分组测试及 webpack 生产构建通过。最后的弹窗焦点/标题修复再次通过 lint 和生产构建。
- 隔离本地服务实际接收 HTTP 请求：Responses 入站直接选 Responses；Chat 非流式和紧随其后的流式请求均经历 Chat 500 → Responses 成功。核对了路径、模型别名、Bearer 鉴权与最终正文。这个场景发现并修复了中途失败误冷却共享 Key 的问题，已加回归测试；所有接口耗尽时仍按原规则记录冷却。
- 实际桌面浏览器完成新建两接口渠道并保存；核对弹窗关闭后消失、标题关联存在、焦点返回触发按钮。390×640 的导出 DOM/CSS 静态渲染核对标题、滚动内容区及固定操作按钮；这不代表已经完成手机触控、缩放及全部键盘路径的端到端验收。
- 本轮没有访问真实公益站做能力探测，也未部署实验站。需要发布新镜像后再验证真实渠道；旧版本镜像不包含这些工作树改动。

### 翻译插件并发请求的 400 回退补修

2026-10-08 只读抽查实验站 25 条 `gpt-5-nano` 分组日志：12 条成功、13 条失败，成功记录最多经历 8 次尝试。失败中 7 条为思考参数不兼容、3 条为内部模型检查点不可访问，其余 3 条仅有 `3051` 或 `bad_response_status_code`。这只是抽样，不代表全站长期成功率。入站正文确认插件显式传入 `reasoning_effort=minimal`；分组有 273 个候选条目，不能假定它们均支持同一思考参数。

补修只识别明确的思考参数拒绝（包含定位到该字段的枚举校验错误）与检查点不可访问通知。参数不兼容时保留请求并换候选，不静默降档，也不记模型故障熔断；未知错误不因缺乏说明就统一放宽。仍受总尝试预算、已输出不重试、确定性内容/上下文错误与渠道显式不可重试规则约束。

回归先复现了旧逻辑 `none (bad request, client error)`，修复后覆盖同渠道换模型、跨渠道、流式/非流式、11 个同时在途请求以及预算耗尽。并发测试使用真实 Handler 和模拟 HTTP Transport，不消耗线上额度。relay 全回归、生产 jsoniter 标签下的新增测试及 Go 构建通过；没有修改线上渠道或插件配置，线上生效仍需发布部署。

## 设计来源

- [OpenAI Chat 流式事件](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events)
- [Cloudflare OpenAI 兼容接口](https://developers.cloudflare.com/workers-ai/configuration/open-ai-compatibility/)
- [Cloudflare 原生 REST](https://developers.cloudflare.com/workers-ai/get-started/rest-api/)
- [Cloudflare 模型目录](https://developers.cloudflare.com/api/resources/ai/subresources/models/methods/list/)
- [MiMo Chat 规范](https://mimo.mi.com/docs/en-US/api/chat/openai-api)
- [MiMo 错误码](https://mimo.mi.com/docs/en-US/api/guidance/error-codes)
- [Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create)
- [Gemini API](https://ai.google.dev/api/generate-content)

这些是设计依据，不是对每个公益站实际实现的认证。公益站是否已转换成标准协议、目录是否可用、特有参数是否支持，均不根据域名或品牌猜测。
