# 系统提示消息与 Agent 工具

## 1. 现在的入口

每次分析开始时（输入框左边的小魔法按钮，或采集器标记有更新后的自动分析），
`panels.go` 的 `startAnalysis` 先清空旧会话，再插入一条 `system` 消息：

```go
a.beginAnalysisSession()
a.addMessage(&chatMessage{
    Role: "system",
    Text: a.systemPrompt(),
    Done: true,
    TS:   time.Now(),
})
a.sendChat(a.startPrompt())
```

`addMessage` 会把它写进当天的 `chat.jsonl`（`role:"system"`）。重新打开这一天
时 `loadTranscript` 会读回来；重新分析时 `beginAnalysisSession` 清掉旧对话，
再写新的一条。

这条消息**不再出现在消息列表里**：`chatRows` 会跳过 `system`。聊天栏顶部
是 Messages 式的蓝色头像 + 玻璃胶囊（`Stilday ›`），外面再包一层
**整块可点的不可见区域**（不画外框）；点任意位置打开中间弹窗
`systemPromptModal`：

```go
card := ui.Column(c).Padding(8, 10).Radius(20).Gap(0).AlignItems(ui.Center).
    Background(t.Background.Alpha(0.65)).
    Border(1, t.Border.Alpha(0.4)).
    Cursor(ui.CursorPointer).Label("系统提示").Tooltip("查看系统提示")
// avatar + pill inside
if card.Clicked() {
    a.chat.systemOpen = true
}
```

弹窗用 `a.promptForDrawer()` 取最近一条 `system` 消息；如果这一天还没有
记录（旧对话），就回退到当前的 `a.systemPrompt()`，所以不会是空白的。
提示语放在 720×560 的居中浮层、可滚动的 `ui.Scroll` 里，可选中复制；
点遮罩、关闭按钮或 Escape 收起。

弹层里 `a.latestSystemPrompt()` 找最近一条 `system` 消息，正文可滚动、可选中
复制。默认关闭，`a.chat.systemOpen` 在换天/重新分析时重置。

## 1b. 用户消息与当天截图

分析触发仍然是一条普通用户消息，但文字不是技术指令。技术任务（看合集、
概述、`stilday_save_day`）已经移到 `prompts/system-prompt.md` 的
「The daily analysis」。

第一句话由 `greeting.go` 的一次性 Pi 生成：在临时目录单独启动一个
`NoTools` 会话，把当天**全部**合集截图作为图片发过去，让它以用户的口吻
写一句给好朋友的开场白。等待期间聊天里先显示截图 + 三个点的输入动效；
拿到后直接带着截图发送，不再做输入框逐字动画。

第一条分析消息因此带 `Images > 0`，聊天里显示当天全部合集的缩略图
（`a.chat.sheets`）。资料栏不再显示合集缩略图，只保留每张照片的大图
缩略图。

## 2. 提示语写在哪

静态部分在：

```
prompts/system-prompt.md
```

`prompt.go` 用 `//go:embed` 把它编进程序：

```go
//go:embed prompts/system-prompt.md
var systemPromptBase string
```

`chat_prompt.go` 的 `systemPrompt()` 在静态部分后面追加动态内容：

```go
b.WriteString(strings.TrimSpace(promptBase()))
b.WriteString("\n\n")
fmt.Fprintf(&b, "今天是 %s。\n", a.chat.day)
fmt.Fprintf(&b, "当天的文件夹是 %s。\n\n", a.chat.dir)
// collectors, manifest, photo count ...
```

所以：

- 想把提示语写长，直接改 `prompts/system-prompt.md`。
- 改完需要重新构建（`/tmp/Stilday.app` / GoLand 重跑）；嵌入文件在构建时生效。
- 特殊消息会自动显示完整结果；长提示语默认折叠，点「查看」展开。
- 动态的今天、目录、采集器、照片数仍在代码里追加，不需要写进文件。

### `stilday_save_day` 的入参是生成的

「记录」一节里的入参类型定义不是手写的。`prompts/system-prompt.md` 里只有一行
占位符：

```
{{stilday_save_day_args}}
```

`promptBase()`（`prompt.go`）和 `systemPrompt()` 在运行时用 `day_log_schema.go` 的
`dayLogArgs` 把它替换成 TypeScript 类型定义（含注释）。同一棵树还会序列化成
JSON Schema，经 `STILDAY_DAY_LOG_SCHEMA` 传给扩展，`pi/stilday.ts` 用它建
TypeBox 参数。所以提示语描述的形状就是工具实际接受的形状，两边不会不一致。

- 加字段、改说明、改可选性：只改 `day_log_schema.go` 的 `dayLogArgs`。
- 固定取值用 `enumNode`：提示语里渲染成联合类型，扩展的 schema 里是 enum。
- 类型定义里的注释就是字段的 `description`，会同时出现在提示语和工具 schema 里。
- 新增一节不需要生成的正文（比如典型场景举例），直接写在
  `prompts/system-prompt.md` 里。

## 3. Agent 的工具

Stilday 的 Agent 是 Pi。Pi 自己的设置（见 Pi 的 `docs/settings.md`）里：

- `defaultTools` 默认是 `read`、`bash`、`edit`、`write`。
- 其他内置工具还有 `powershell`、`grep`、`find`、`ls`；`codemode` 和
  `tool_search` 是内置扩展注册的工具。
- 这些默认值只在没有 `--tools` 覆盖时生效。

Stilday 在 `chat_agent.go` 的 `startAgent` 里传了一个**纯名称列表**：

```go
Tools: []string{"day_overview", "read", "stilday_save_day"},
```

纯名称列表会**替换** Pi 的 `defaultTools`，所以当前这个 Agent 实际只有三个工具：

| 工具 | 来源 | 作用 |
| --- | --- | --- |
| `day_overview` | Stilday 自定义（`pi/stilday.ts`） | 取当天的编号合集和照片列表；需要再看照片时调用 |
| `read` | Pi 内置 | 读 `manifest.json`，再按照片的 `read` 路径读 1600px JPEG 预览 |
| `stilday_save_day` | Stilday 自定义（`pi/stilday.ts`） | 保存当天的日志；每次调用用新列表替换当天已有的记录；应用侧映射成年度 JSONL 的 `occurred_at` / `updated_at` / `day` / `kind` / `summary` |

当前**没有**启用：`bash`、`edit`、`write`、`grep`、`find`、`ls`、
`powershell`、`codemode`、`tool_search`。也就是说，模型不能执行 shell、改文件，
只能看当天素材、读照片，并通过 `stilday_save_day` 写当天的日志。

`chat_message.go` 里还有 `photo_detail` 的旧显示映射，但扩展没有注册这个工具；
照片细节实际走 `read`，读 `manifest.json` 里给出的 `read` 路径。

注意：特殊消息显示的是 Stilday 用 `a.systemPrompt()` 生成的提示语；Pi 还会在
它之上加自己的基础指令和工具定义，那部分目前不在这条消息里。

## 4. 相关代码位置

- `panels.go` — `startAnalysis` 插入 system 消息；`beginAnalysisSession` 清空旧会话。
- `chat.go` — `chatHeader` / `systemPromptModal`（头像 + 玻璃按钮 + 弹层）。
- `chat_prompt.go` — `systemPrompt` 拼接静态 + 动态。
- `chat_store.go` — `persistChat` / `loadTranscript` 持久化。
- `prompt.go` — 嵌入 `prompts/system-prompt.md`，填 `stilday_save_day` 那一节。
- `day_log_schema.go` — `stilday_save_day` 入参的唯一声明：生成提示语的类型定义和扩展的 schema。
- `pi/stilday.ts` — `day_overview`、`stilday_save_day` 两个自定义工具。
- `chat_agent.go` — `startAgent` 传 `Tools` 白名单给 Pi。
