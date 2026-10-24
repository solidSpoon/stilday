# Stilday 架构

> 这份文档描述重构后的边界。改代码前先读「规则」一节；它记录的是约束，不是
> 风格偏好。

## 一句话

一个 Go 进程 + MyGo 原生 UI。一天的照片和对话在**工作目录**里，AI 写出的
事件在**年度 JSONL** 里；界面只组合状态，文件读写在专门的层里发生。

## 分层

| 层 | 文件 | 职责 |
| --- | --- | --- |
| 产物格式 | `internal/artifact` | `Entry`、一行 JSONL 的编解码、按年文件、旧格式迁移 |
| 配置与坐标 | `config.go`、`clock.go` | 配置默认值/校验、存储布局、时段、日期与时钟换算 |
| 采集器 | `collector.go` | 插件契约、全局/按天状态、指纹、manifest 同步 |
| 相册插件 | `collector_photos.go`、`photos.go`、`photo_*.go` | 照片发现、EXIF/PhotoKit、预览、合集、manifest、范围同步 |
| 采集调度 | `collector_runner.go` | 单次/全部运行、进度、自动采集、待分析判定 |
| 调度 | `internal/schedule` | 每日 job 的注册、到点启动、上次运行记录；每个分析任务的 pending/running/done/cancelled/failed、取消/重新启动；不碰窗口和磁盘布局 |
| AI | `agent/rpc.go`、`chat_agent.go`、`chat_analysis.go`、`chat_prompt.go`、`greeting.go`、`pi.go`、`models.go`、`provider.go`、`piinstall.go` | Pi 子进程、事件流、prompt、首句、运行时下载、模型列表 |
| 对话 | `chat.go`、`chat_store.go`、`chat_message.go`、`chat_composer.go` | 一天的会话状态、transcript、消息/工具行、输入条 |
| 页面 | `app.go`、`analysis.go`、`schedule.go`、`sidebar.go`、`calendar.go`、`board.go`、`day.go`、`panels.go`、`settings.go`、`collectorpage.go` | 窗口壳、导航、日历、事件板、一天三栏、设置、采集器页 |
| 视觉 | `chrome.go`、`kind.go`、`icons.go` | 主题、页面壳、分组、玻璃按钮、事件配色与标签 |
| 扩展 | `extension.go`、`pi/stilday.ts` | 释放给 Pi 的三个工具 |
| 对外查询 | `mcp_server.go` | 本机 MCP 端点（127.0.0.1）、`search_events` 工具、端口与启停 |

目录就是包边界：

```
stilday/
  main.go app.go sidebar.go calendar.go day.go panels.go board.go
  chat*.go settings.go collectorpage.go chrome.go kind.go …   → package main（UI）
  internal/artifact   事件行 + 年度 JSONL（不依赖任何 UI）
  internal/schedule   每日 job + 按天分析任务的状态机
  internal/collector* 还在 main 里：插件契约需要 ui.Context 和 photo 类型
  agent/              Pi RPC（独立包）
```

依赖方向是单向的：`main` → `internal/*` → 标准库。`artifact` 和 `schedule`
不知道窗口存在；采集器的运行编排仍在 main，因为它要驱动页面和 photo。
采集器、调度器、agent 只拿值或回调，不拿 `*app`。

## 规则

1. **采集器是插件，不是 app 的一部分。** 它只收到 `collectorEnv`（当天目录、
   插件自己的目录、照片来源、时段）和 `collectorView`（当天 manifest）。设置
   由插件自己持有：`LoadSettings` / `SaveSettings` / `SettingsChanged` 读写
   `<存储>/plugins/<id>/settings.json`，指纹也由插件的 `Fingerprint` 给（默认
   实现是文件夹哈希）。新增采集器时不要往接口里塞 `*app`；它需要的新输入放进
   env。
2. **后台线程只做文件和 CPU。** 回写状态一律 `a.win.Update(...)`；窗口不存在
   （测试）时同步内联执行，测试因此是确定性的。不要在工作线程里读 `a.cfg`、
   `a.chat`——在 UI 线程算好 `collectorEnv`、`piTarget` 再传进去。
3. **渲染期间不写文件。** 采集器页的输入直接绑定到持久字段，构建结束时只标记
   `collectorAppDirty` / `collectorSettingsDirty`，再由 `scheduleCollectorFlush`
   排到本帧之后；下一帧开头的 `a.tick()` 也会兜底 `flushCollectorPage()`。
   设置页仍是显式保存。
4. **一天一份会话，换天不停别的天。** `a.chats` 按 `YYYY-MM-DD` 保存每天
   的 `chatState`，`a.chat` 只指向选中那天；`openDay` 取或建当天的状态（从
   `chat.jsonl` 和 manifest 载入），不关闭、不重置其他天。换天不停止正在跑
   的 turn，切回来看到的就是实时状态。重新分析走 `beginAnalysisSession` /
   `restartConversation`，它们都接收那天的 `*chatState`。
5. **日历的灰底看素材，不看文件夹。** 一天有产物条目，或任一插件当天的
   `Fingerprint` 非空，才算有内容。指纹本来就是插件对「我这一天采到了什么」
   的答案：默认实现是文件夹哈希，文件夹里没东西就是空字符串，所以不用另加
   「这天空不空」的方法，日历读一遍当天的 `collectors.json` 就能替所有插件
   回答。只有对话或系统提示的日记（`chat.jsonl`/`system.md`）不算素材；
   `pruneContentlessDays` 只清「只有 collectors.json/manifest.json」的目录。
6. **分析任务是调度器里的状态机。** 每个待分析的天注册成
   `analysis:<day>` 任务，状态（pending/running/done/cancelled/failed）和
   `Due` 只存在于 `internal/schedule` 和 `<存储>/schedule.json`；UI 只读
   `Tasks()/RunningTask()` 和 `CanStart/CanPause/CanCancel`，通过
   `Cancel/Retry/Restore/StartAll/Pause/CancelAll` 改状态。任务的 `Run` 回调
   由 app 提供，启动那天的 `chatState`。`syncAnalysisTasks` 把「指纹变了且
   没被取消」的天补成任务，`Due` 是下一个每日分析时间；采集器只更新指纹，
   不写任务。采集器**开始**执行时 `takeConversationSlot()` 把正在跑的分析
   `Interrupt` 回 pending（排到下一个每日分析时间），不标取消，扫完重算时
   还会回来。
7. **产物行只由 `entryRecord` 编解码。** 写入用 `encoding/json`（关闭 HTML
   转义）；`ts`、`data.summary` 只作为读兼容存在。不要再手拼 JSON。
8. **路径只由少数函数拼。** `config.workDayDir` / `config.artifactPath` /
   `artifactFile` 是存储布局的唯一来源；日期键统一 `dayFormat`（`dayKey` /
   `parseDayKey`）。
9. **页面身份用 `pageID` 常量**，不要比较字符串字面量。
10. **会话只有一个执行槽。** `a.turn`（当前持有者）、各天的 `preparing`、
    `greetingPending`、`collecting` 共用这个槽；手动发消息、手动分析、采集、
    定时任务都先问 `chatIdle()` / `takeConversationSlot()` / `claimTurn()`，
    不要在入口各写一套条件。规则：
    - 手动聊天占着槽时，采集器不抢（`chatIdle()` 为假）；
    - 分析任务占着槽时，采集器可以抢：`interruptAnalysis()` 停掉它并把任务
      退回 pending，因为扫描会换掉正在分析的素材；
    - 用户消息永远不丢、也不插队：槽被占时消息立刻进那天的会话，prompt 进
      `a.pending`，`pumpConversation()` 在当前 turn 结束（或照片准备/采集
      结束）后先泵用户消息，再让调度器启动下一天的分析。

## 任务调度

采集、分析、手动动作共用一个执行槽：**串行，但没有先后依赖**。

- **采集插件**按各自的每日时间跑（手动「立即执行」一样）。开始执行时
  `takeConversationSlot()` 会 `Interrupt` 正在分析的那天：停掉它，任务退回
  pending 并排到下一个每日分析时间；待分析的天不标取消。每次执行结束
  （`finishCollector`；相册的范围同步是整段结束）都重新扫描变更天、同步任务，
  最后才 `pumpConversation()`。手动聊天占着槽时采集器等它结束。
- **分析任务**是调度器里的 `analysis:<day>`。`syncAnalysisTasks` 给每个
  「指纹变了、任务没被取消」的天建任务，`Due` 是下一个 `cfg.AutoTime`；
  到点后调度器只启动一个：`startAnalysisDay` 在那天自己的 `chatState` 里跑，
  不切 `a.day`/`a.page`。页面正好显示那天时，事件和消息实时长出来。
  行末的「立即执行」和右侧「启动所有」把等待的任务改成现在到期。
- **取消 / 重新启动**都是任务状态变更，跟着 `schedule.json` 跨启动保存。
  取消所有会把等待和正在跑的任务都取消；重新启动让取消或失败的一天回到
  「待分析」，等下一个分析时间。
- **手动动作**（发消息、重新分析）先占槽；消息在槽被占时排队，turn 结束后
  先于下一天的分析跑。槽的持有者由 `a.turn` 表示，`aiWorkingDay()` 给出正在
  回复/分析的那天：日历给它强调环，侧栏显示「对话中」或「分析中」。

所以「采集完再分析」不是依赖，只是碰巧串行：采集到点就跑，分析到点看当前
任务，两边互不等待，只保证不同时跑。新增插件时，执行结束的收尾走
`finishCollector`，不要自己另起一套重算或另抢一次槽。

调度本身在 `internal/schedule`：每个固定 job 有 `ID`、`Due(now)` 和
`Run() bool`，`Run` 返回 false 表示这一拍占不到槽，调度器下一拍再试；分析的
每日任务还有 `State`、`Due` 和 `Run(day) bool`，状态和最后运行时间都在
`<存储>/schedule.json` 里，所以任务不需要为了「今天跑过没有」在数据目录里留
任何东西。应用在 `schedule.go` 里注册固定 job：每个插件一个 `collector:<id>`；
按天任务由 `analysis.go` 同步。新增插件时只需依赖 `collectorList`，会自动得到
自己的 job。

## 数据流

```
采集器 Run ──► work/YYYY/MM/DD/<folder> ──► 插件自己的 Fingerprint
                                              │
抓到的天 ──► collectors.json（fingerprint / analyzed_fingerprint）
                                              │
                     changedDays ──► schedule analysis:<day> ──► 那天自己的 chatState
                                                      │
                          Pi (RPC) ──► stilday_save_day ──► store.save
                                                      │
                                    artifacts/YYYY.jsonl ──► 日历标记 / 事件板
```

## 对外查询

`mcp_server.go` 在设置页打开后监听 `127.0.0.1:<端口>/mcp`，用官方
`modelcontextprotocol/go-sdk` 的 streamable HTTP（stateless、JSON 响应），
只提供一个 `search_events` 工具，读 `artifactsRoot` 下的年度 JSONL。服务名和
说明只描述「主人有哪些信息」，不要写这些信息是从什么材料里得来的；查询按
`day`、`kinds`、`text` 过滤，返回 `occurred_at`、`day`、`kind`、`summary`。
端点只绑本机，没有鉴权；改端口或关掉都在设置页。

## 生命周期

- **换天**：`selectDay` → `loadEntries` → `openDay`；`openDay` 按天取或建
  `chatState`（磁盘上的 transcript、collector 状态和 manifest），其他天的状态
  和 agent 原样留着。
- **重新分析**：清空当天产物行、`chat.jsonl`、`.pi/`，保留照片与 manifest；
  那天的 `chatState` 换新会话后重新生成开场白并发送。
- **后台分析**：调度器的 `analysis:<day>` 任务调用 `startAnalysisDay`，同样
  在这天的 `chatState` 里跑；它不改选中的天或页，页面显示那天时自然刷新。
- **采集一次**：`beginCollection` → 清空插件文件夹（插件说 `KeepsFolder` 就
  不清）→ `Run` → 插件自己的 `Fingerprint` → `finishCollection`；状态落在当天
  `collectors.json`，并同步进 `manifest.json`。一天没采到任何东西就不写这两份
  状态、也不留目录；调度器的时间记在 `<存储>/schedule.json`，不再靠空目录。
  插件设置由插件自己从 `<存储>/plugins/<id>/settings.json` 读、自己写回。
- **夜间链路**：`internal/schedule` 按每个 job 的时间启动——插件到点采集
  （`runScheduledCollector`），到每个分析任务的 `Due` 时启动那天的
  `chatState`（`runAnalysisTask`）。

## 已知边界

- `store.load` 每次读整年文件；日历 `loadAll` 扫描全部年份。数据量真正变大
  时再加按年缓存，但要先设计失效（写入/迁移/外部修改）。
- 只有「相册照片」一个采集器；「一次采多天」是调度特例
  （`syncRangeCollector`），不是插件契约的一部分。
- `openDay`、`loadBoard` 在渲染里做受保护的懒加载，这是 MyGo 的惯例；它们
  在状态已经就绪时立即返回。`a.chats` 在一次运行里只增不减，一天的 agent
  会一直留到应用退出或设置换存储。
- Pi 的 `agent.Client.Steer/GetState` 是预留的协议表面，应用目前不用。

## 验证

```sh
GOCACHE=$PWD/.gocache go test ./...        # 逻辑 + ui.NewTester
GOCACHE=$PWD/.gocache go test -race ./...
GOCACHE=$PWD/.gocache go tool mygo vet .
STILDAY_SHOTS=1 go test -run TestShots     # 截图写 /tmp，和上一版像素对比
```

截图里的时间、滚动条、临时路径每次都不同；比较时先和同一版本跑两次的差异
对齐，剩下的才是真正的视觉变化。
