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
| 分析队列 | `internal/analysis` | 待分析天的排队、暂停、取消、重试；不碰窗口和磁盘 |
| AI | `agent/rpc.go`、`chat_agent.go`、`chat_prompt.go`、`greeting.go`、`pi.go`、`models.go`、`provider.go`、`piinstall.go` | Pi 子进程、事件流、prompt、首句、运行时下载、模型列表 |
| 对话 | `chat.go`、`chat_store.go`、`chat_message.go`、`chat_composer.go` | 一天的会话状态、transcript、消息/工具行、输入条 |
| 页面 | `app.go`、`sidebar.go`、`calendar.go`、`board.go`、`day.go`、`panels.go`、`settings.go`、`collectorpage.go` | 窗口壳、导航、日历、事件板、一天三栏、设置、采集器页 |
| 视觉 | `chrome.go`、`kind.go`、`icons.go` | 主题、页面壳、分组、玻璃按钮、事件配色与标签 |
| 扩展 | `extension.go`、`pi/stilday.ts` | 释放给 Pi 的三个工具 |

目录就是包边界：

```
stilday/
  main.go app.go sidebar.go calendar.go day.go panels.go board.go
  chat*.go settings.go collectorpage.go chrome.go kind.go …   → package main（UI）
  internal/artifact   事件行 + 年度 JSONL（不依赖任何 UI）
  internal/analysis   分析队列状态机（只依赖 artifact 的日期解析）
  internal/collector* 还在 main 里：插件契约需要 ui.Context 和 photo 类型
  agent/              Pi RPC（独立包）
```

依赖方向是单向的：`main` → `internal/*` → 标准库。`artifact` 不知道窗口存在，
`analysis` 不知道磁盘存在；采集器的运行编排仍在 main，因为它要驱动页面和 photo。
采集器、队列、agent 只拿值或回调，不拿 `*app`。

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
4. **一天的会话只有一个重置入口。** 换天走 `resetForDay` → `chatState.reset`；
   重新分析走 `restartConversation` + `clearDayAnalysis`。不要再手写一串
   字段清零——之前三处拷贝已经开始漂移。
5. **分析队列是状态机。** UI 只读 `a.analysis` 的字段和调用
   `Pending/CanStart/CanPause/CanCancel/StartPending/Pause/CancelAll/Finish`；
   `Running`、`Current`、`Queue` 的迁移只发生在 `internal/analysis`。采集器
   只更新指纹并刷新待分析列表，不写队列；到点批量启动直接 `StartPending`，
   两边没有先后依赖。
6. **产物行只由 `entryRecord` 编解码。** 写入用 `encoding/json`（关闭 HTML
   转义）；`ts`、`data.summary` 只作为读兼容存在。不要再手拼 JSON。
7. **路径只由少数函数拼。** `config.workDayDir` / `config.artifactPath` /
   `artifactFile` 是存储布局的唯一来源；日期键统一 `dayFormat`（`dayKey` /
   `parseDayKey`）。
8. **页面身份用 `pageID` 常量**，不要比较字符串字面量。
9. **会话只有一个执行槽。** `chat.busy`、`chat.preparing`、
   `chat.greetingPending`、`collecting` 共用这个槽；手动发消息、手动分析、
   立即执行采集、定时采集、定时分析和「启动所有」都先问 `chatIdle()` /
   `canStartWork()`，不要在入口各写一套条件。分析队列自己的 `startAnalysis`
   只看 `chatIdle()`，因为它就是槽的持有者。

## 数据流

```
采集器 Run ──► work/YYYY/MM/DD/<folder> ──► 插件自己的 Fingerprint
                                              │
抓到的天 ──► collectors.json（fingerprint / analyzed_fingerprint）
                                              │
                     changedDays ──► analysisQueue ──► 选中那天
                                                      │
                          Pi (RPC) ──► stilday_save_day ──► store.save
                                                      │
                                    artifacts/YYYY.jsonl ──► 日历标记 / 事件板
```

## 生命周期

- **换天**：`selectDay` → `closeAgent` → `loadEntries` → `resetForDay`；下一次
  `dayPage` 的受保护 `openDay` 从磁盘载入 transcript、collector 状态和 manifest。
- **重新分析**：清空当天产物行、`chat.jsonl`、`.pi/`，保留照片与 manifest；
  `chatState` 换新会话后重新生成开场白并发送。
- **采集一次**：`beginCollection` → 清空插件文件夹（插件说 `KeepsFolder` 就
  不清）→ `Run` → 插件自己的 `Fingerprint` → `finishCollection`；状态落在当天
  `collectors.json`，并同步进 `manifest.json`。插件设置由插件自己从
  `<存储>/plugins/<id>/settings.json` 读、自己写回。
- **夜间链路**：每个启用插件到点采集（`maybeAutoCollect`）；到每日分析时间，
  只把「启用插件都已 done 且指纹有变化」的天排队（`maybeAutoAnalyze`）。

## 已知边界

- `store.load` 每次读整年文件；日历 `loadAll` 扫描全部年份。数据量真正变大
  时再加按年缓存，但要先设计失效（写入/迁移/外部修改）。
- 只有「相册照片」一个采集器；「一次采多天」是调度特例
  （`syncRangeCollector`），不是插件契约的一部分。
- `openDay`、`loadBoard` 在渲染里做受保护的懒加载，这是 MyGo 的惯例；它们
  在状态已经就绪时立即返回。
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
