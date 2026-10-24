# Pi × Stilday 集成调研

> 状态：已实现，本文保留为当时的调研。当前实现分散在 `chat_agent.go`、
> `chat_prompt.go`、`agent/rpc.go`、`pi/stilday.ts`，分层见
> `docs/architecture.md`。三个工具是 `day_overview`、`read`、
> `stilday_save_day`；本文后半的 `stilday_result`、按天
> `artifacts/YYYY/MM/DD.jsonl` 等是最初方案，保留作为参考。

## 结论

Pi 是一个 **agent harness**（Node/TypeScript），不是可以直接嵌入 Go 的 LLM
库。对 Stilday 最合适的集成方式是：

**Stilday 起一个 `pi` 子进程 → 用 JSON 事件流观察执行 → 模型通过自定义工具
`stilday_result` 交付 JSON → 结果写回当天的 JSONL。**

不要把 Pi 编进 Go 进程，也不要在 Go 里实现 agent 循环。父进程只做三件事：
传指令、读事件、收结果。

```
Stilday (Go)
│
├─ agent.Task{ instruction, schema, tools, cwd, model, timeout }
│
├─ exec: pi --mode json -p --no-session \
│         -e ~/.stilday/pi/stilday-result.ts \
│         --tools read,grep,stilday_result \
│         --model sonnet \
│         "整理今天的事件，并按给定的 JSON Schema 返回"
│
│    stdout: 一行一个 JSON 事件
│      ├─ message_update            token 流，可实时显示
│      ├─ tool_execution_start      模型在用 read / grep
│      ├─ tool_execution_end        stilday_result 的 details 就是结果
│      └─ agent_settled             这次运行结束
│
└─ 写回 <storage>/artifacts/YYYY/MM/YYYY-MM-DD.jsonl
     {"occurred_at":…,"updated_at":…,"day":…,"kind":"task.result","summary":…}
```

## Pi 的四种接口

| 方式 | 进程 | 适用 |
| --- | --- | --- |
| 交互 TUI | `pi` | 人直接用，不适合应用 |
| Print | `pi -p "…"` | 只要最后一段文本，最简单 |
| **JSON** | `pi --mode json -p "…"` | **应用集成首选**：一次性运行，但事件是流式的 |
| RPC | `pi --mode rpc` | 长驻、多轮、可中断/steer，需要自己管理协议 |
| SDK | `@earendil-works/pi-coding-agent` | 在 Node/Bun 里嵌入；Go 用需要另起 Node 服务 |

**JSON 模式不是“结构化输出”**：它输出的是 agent 事件流，模型最后仍可能返回
散文。要保证拿到 JSON，需要下面这个自定义工具，或者自己解析最后一条消息。

## 结构化结果：`stilday_result` 工具

Pi 官方示例 `examples/extensions/structured-output.ts` 用 `terminate: true`
让模型以一次工具调用结束，结果放在 tool result 的 `details` 里。我们把这个
思路做成通用的：**每次任务通过环境变量传入 JSON Schema**，工具参数就按该
Schema 声明。

`~/.stilday/pi/stilday-result.ts`（随应用 embed，首次运行时释放）：

```ts
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const schema = process.env.STILDAY_RESULT_SCHEMA
  ? JSON.parse(process.env.STILDAY_RESULT_SCHEMA)
  : { type: "object", additionalProperties: true };

export default function (pi: ExtensionAPI) {
  pi.registerTool(
    defineTool({
      name: "stilday_result",
      label: "Stilday Result",
      description:
        "Return the final result of the task as JSON. Use this as your last action.",
      promptSnippet: "Deliver the final result as JSON with stilday_result",
      promptGuidelines: [
        "Finish the task by calling stilday_result exactly once.",
        "Its arguments must be the JSON object the task describes.",
        "Do not repeat the JSON in a normal assistant message.",
      ],
      parameters: Type.Unsafe<Record<string, unknown>>(schema),
      async execute(_id, params) {
        return {
          content: [{ type: "text", text: "Result recorded." }],
          details: params,
          terminate: true,
        };
      },
    }),
  );
}
```

关键点：

- `parameters` 来自任务的 JSON Schema，模型看到的就是目标结构。
- `terminate: true` 让 agent 停在这次工具调用，不再多花一轮 LLM。
- `details` 原样进入 `tool_execution_end.result.details`。
- 模型如果没调用工具，Go 侧还可以回退到解析最后一条 assistant 文本，
  再校验 JSON；失败时带错误重试一次。

## Go 侧骨架

```go
type Task struct {
    Instruction string
    Schema      json.RawMessage // JSON Schema
    Tools       []string        // 默认 read,grep
    CWD         string
    Model       string
    Timeout     time.Duration
}

type Result struct {
    Data    json.RawMessage
    Text    string
    Usage   Usage
    Events  []Event
}

func (r *PiRunner) Run(ctx context.Context, t Task) (Result, error) {
    ext := r.resultExtension() // ~/.stilday/pi/stilday-result.ts
    args := []string{
        "--mode", "json", "-p", "--no-session",
        "-e", ext,
        "--tools", strings.Join(append(t.Tools, "stilday_result"), ","),
    }
    if t.Model != "" {
        args = append(args, "--model", t.Model)
    }
    args = append(args, t.Instruction)

    cmd := exec.CommandContext(ctx, r.Bin, args...)
    cmd.Dir = t.CWD
    cmd.Env = append(os.Environ(), "STILDAY_RESULT_SCHEMA="+string(t.Schema))
    stdout, _ := cmd.StdoutPipe()
    var stderr bytes.Buffer
    cmd.Stderr = &stderr

    if err := cmd.Start(); err != nil { return Result{}, err }

    dec := json.NewDecoder(stdout) // 不要用 bufio.Scanner 的默认 64K 上限
    var out Result
    for {
        var ev Event
        if err := dec.Decode(&ev); err == io.EOF {
            break
        } else if err != nil {
            return out, fmt.Errorf("pi stream: %w", err)
        }
        out.Events = append(out.Events, ev)
        switch ev.Type {
        case "tool_execution_end":
            if ev.ToolName == "stilday_result" && !ev.IsError {
                out.Data = ev.Result.Details
            }
        case "message_end":
            if ev.Message.Role == "assistant" {
                out.Text = textOf(ev.Message)
                out.Usage = ev.Message.Usage
            }
        }
    }
    if err := cmd.Wait(); err != nil {
        return out, fmt.Errorf("pi: %w: %s", err, stderr.String())
    }
    if len(out.Data) == 0 {
        return out, ErrNoResult
    }
    return out, nil
}
```

事件里只需要三个字段：

```go
type Event struct {
    Type     string          `json:"type"`
    ToolName string          `json:"toolName"`
    IsError  bool            `json:"isError"`
    Result   struct {
        Details json.RawMessage `json:"details"`
    } `json:"result"`
    Message struct {
        Role    string `json:"role"`
        Content []struct{ Type, Text string } `json:"content"`
        Usage   Usage  `json:"usage"`
    } `json:"message"`
}
```

## 接进 Stilday 的产品形态

建议先做“一次性任务”，不要一开始就做聊天：

1. **侧栏加一页 “AI 任务”**：任务列表 + 指令输入 + Run/Stop + 结果卡片。
2. **内置几个任务**（每个都有固定 JSON Schema）：
   - 今日总结：`{ summary, highlights[], focus_minutes, open_threads[] }`
   - 剪贴板归类：`{ categories: [{name, items[]}] }`
   - 专注时段：`{ blocks: [{start, end, app, minutes}] }`
   - 自定义：用户自己贴指令和 Schema
3. **运行时把事件流回 UI**：`message_update.delta` 显示文本，`tool_execution_*`
   显示“正在读文件”，最后展示 pretty JSON。
4. **结果写回当天 JSONL**，和其他采集点平级：

   ```json
   {"occurred_at":"…","updated_at":"…","day":"…","kind":"task.result","summary":"…"}
   ```

   这样 AI 的结论本身也成为上下文：首页能看到、能搜、能再交给下一个 AI。
5. **任务定义存 `~/.stilday/tasks.json`**，后续做 UI 编辑；先内置即可。

## 什么时候不该用 Pi

如果任务只是“把这段文字变成 JSON”，没有读文件、跑命令、查上下文的需求，
直接用模型的 structured output（JSON Schema）会比 Pi 简单得多：少一个
Node 进程、少一层事件协议，也更便宜。

Pi 的价值在于 **执行**：它能读 `~/.stilday`、grep 当天事件、调用 MCP、
按需多步推理。需要这些能力时，再用上面的方案。

## 运行环境与安全

- Pi 需要 **Node ≥ 22.19**（或它自己的 standalone binary）。`exec.LookPath("pi")`
  找不到时，UI 显示安装命令和 `pi /login` 引导。
- 认证复用 Pi 自己的 `~/.pi/agent`；Stilday 不存 API key。
- Pi **默认没有任何权限沙箱**，以当前用户身份运行。按任务收紧：
  - 只读任务：`--tools read,grep,find,ls`
  - 默认不给 `bash`；需要时再单独开
  - `cwd` 指向任务需要的最小目录（例如 `~/.stilday`）
  - 剪贴板、网页内容是不可信输入，存在 prompt injection；工具越少越好
- `--no-session` 做一次性、互不污染的运行；需要连续追问时再换 `--mode rpc`
  并保留 session。
- 超时用 `context.WithTimeout`；取消就是 kill 子进程。
- stdout 只读 JSONL，stderr 是日志；不要把两者混在一起。
- 事件可能很大（read 结果），不要用默认 `bufio.Scanner`。

## 什么时候升级

| 需求 | 方案 |
| --- | --- |
| 一次性“指令 → JSON”（现在） | JSON 模式 + `stilday_result` |
| 运行中显示 token/工具进度 | 同上，事件本来就是流式的 |
| 多轮追问、运行中 steer、切模型 | RPC 模式，协议见 `docs/rpc.md` |
| 完全掌控 session/tools、避免进程启动开销 | SDK + Node sidecar |
| 分发给别人、不想让对方装 Node | 打包 Pi standalone binary，或 sidecar |

## 分阶段落地

- [x] `agent` 包 + RPC 客户端 + 假 `pi` 的协议测试
- [x] 对话页：流式回复、工具调用、每天一个 Pi 会话
- [x] 照片工作目录、编号合集、`day_overview` / `read` 看细节
- [x] `stilday_save_day` 写回当天 JSONL
- [ ] 用 PhotoKit 直接取照片（现在是文件夹或 Photos 的 `originals`）
- [ ] 更多内置任务、每周回顾

## 参考

- `packages/coding-agent/docs/cli-integration.md`
- `packages/coding-agent/docs/json.md`
- `packages/coding-agent/docs/rpc.md`
- `packages/coding-agent/docs/sdk.md`
- `packages/coding-agent/examples/extensions/structured-output.ts`
- `packages/coding-agent/examples/extensions/subagent/index.ts`（用 JSON 模式
  捕获子 agent 结构化输出的真实例子）
