# AGENTS.md

Stilday 是一个 macOS 上的日记应用：Go + MyGo 原生 UI，AI 对话走 Pi。产品说明在
`README.md`，分层和改代码前要守住的规则在 `docs/architecture.md`。

## 命令

- `go test ./...` — 不打开窗口，跑全部视图和协议测试。
- `make dev-app` — 构建 dev 版（dev 图标、`.dev` identifier、inspector）并打开，
  不监听文件。
- `make release` — 发版：检查分支/工作区/tag，跑测试，构建 dmg，打 tag，推
  `main` 和 tag，再用 `gh release create --generate-notes` 建 release。
- `go tool mygo build` — 正式包，`.app` + `.dmg`。
- **不要跑 `go tool mygo dev`**：它会在每次保存时重建并重启用户正在看的 app。

`make release` 的版本号取自 `mygo.config.ts`；发版前先改版本并提交。没有
Developer ID 时是 ad-hoc 签名。

## 约定

- 系统提示「记录」里的入参类型是生成的：改 `day_log_schema.go` 的 `dayLogArgs`，
  不要手改 `prompts/system-prompt.md` 里由 `{{stilday_save_day_args}}` 填出来的段落。
- `stilday_save_day` 保存的是当天日志的全量：每次调用替换当天已存的条目，所以
  模型必须每次发完整列表；空列表不会清空当天。
- 采集、分析队列、手动动作共用一个会话槽，必须串行：入口统一用
  `chatIdle()` / `takeConversationSlot()`。采集开始会 `analysis.StopRun()`
  （停当前、清队列、待分析不标取消），插件执行结束统一
  `refreshChangedDays()` 重算待分析。详见 `docs/architecture.md` 的「任务调度」。
- 事件的 kind 只有 schema 里那六个；旧 kind 不做兼容，界面上显示灰色和原始英文。
- 设计简单、皮实；不要为假设中的未来需求加抽象。
