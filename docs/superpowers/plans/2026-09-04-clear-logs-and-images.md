# 日志与图片一键清除 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在管理员后台为日志管理和图片管理提供独立的一键彻底清除能力，并安全刷新界面状态。

**Architecture:** 后端在现有管理员路由中新增两个独立 POST 接口。日志清除与现有日志追加共用 `Server.logMu`，调用日志和运行日志按文件清空；图片清除复用既有图片文件识别和目录清理逻辑，并在同一操作中删除标签文件。前端分别在日志页和图片页调用独立 API，经过统一确认弹窗后刷新列表和统计。

**Tech Stack:** Go `net/http`、标准库文件系统、Go `httptest`；Vue 3、TypeScript、现有 `apiClient`、`useConfirmDialog`、Toast、Nanocat UI。

---

## 文件映射

- Modify: `internal/httpapi/server.go` — 注册 `/api/logs/clear` 和 `/api/images/clear`。
- Modify: `internal/httpapi/admin_extra.go` — 实现日志全量清除及运行日志文件统计/清空。
- Modify: `internal/httpapi/admin_media.go` — 实现图片、元数据、标签全量清除。
- Modify: `internal/httpapi/admin_extra_test.go` 或现有 HTTP API 测试文件 — 添加日志清除接口测试。
- Modify: `internal/httpapi/admin_media_test.go` 或现有 HTTP API 测试文件 — 添加图片清除接口测试。
- Modify: `web-vue/src/api/logs.ts` — 增加清除 API 类型和方法。
- Modify: `web-vue/src/api/gallery.ts` — 增加清除 API 类型和方法。
- Modify: `web-vue/src/views/Logs.vue` — 添加“清除全部日志”按钮和清除处理逻辑。
- Modify: `web-vue/src/views/Gallery.vue`、`web-vue/src/views/gallery/galleryOperationsRuntime.ts` — 添加“清除全部图片”按钮、确认、调用和刷新。

### Task 1: 添加日志清除的失败测试

**Files:** `internal/httpapi/admin_extra_test.go` 或现有测试文件

- [ ] **Step 1: 写测试**

创建临时 `DataDir`，写入 `logs.jsonl`、`runtime.log`、`app.log`，构造管理员已通过认证的测试请求，调用清除处理函数或完整路由。断言响应成功、调用日志和运行日志文件为空/不存在，返回的 `entries` 和 `freed_bytes` 大于 0；同时写入一个非日志文件并断言未被删除。

- [ ] **Step 2: 运行失败测试**

运行：`go test ./internal/httpapi -run TestClearAllLogs -count=1 -v`

预期：失败，因为 `/api/logs/clear` 尚未注册或处理函数不存在。

### Task 2: 添加图片清除的失败测试

**Files:** `internal/httpapi/admin_media_test.go` 或现有测试文件

- [ ] **Step 1: 写测试**

在临时 `ImageDataDir` 的嵌套目录中创建 PNG/JPEG 图片及对应 `.meta.json`，创建非图片文件；在 `DataDir` 创建 `image_tags.json`，并在其它目录创建视频文件。调用 `/api/images/clear`，断言图片和元数据、标签文件被删除，空子目录被清理，非图片文件、视频文件和根目录保留，响应返回正确删除计数和释放字节数。

- [ ] **Step 2: 运行失败测试**

运行：`go test ./internal/httpapi -run TestClearAllImages -count=1 -v`

预期：失败，因为 `/api/images/clear` 尚未注册或处理函数不存在。

### Task 3: 实现后端日志清除

**Files:** `internal/httpapi/server.go`, `internal/httpapi/admin_extra.go`

- [ ] **Step 1:** 在 `/api/logs` 路由附近注册 `mux.HandleFunc("/api/logs/clear", s.clearAllLogs)`。
- [ ] **Step 2:** 实现 `clearAllLogs`：管理员校验、POST 校验、在 `s.logMu` 下处理调用日志和四个运行日志路径，统计非空条目/行与字节，使用 `O_TRUNC` 清空，返回 `{ok, files, entries, freed_bytes}`。
- [ ] **Step 3:** 运行 `go test ./internal/httpapi -run 'TestClearAllLogs|Test.*Logs' -count=1 -v`。

### Task 4: 实现后端图片清除

**Files:** `internal/httpapi/admin_media.go`

- [ ] **Step 1:** 在 `adminImages` 增加 `POST /clear` 分支。
- [ ] **Step 2:** 递归删除配置图片目录内的图片及 `.meta.json`，删除 `image_tags.json`，清理空目录，返回删除计数和字节数；保留根目录、非图片文件、视频目录及其它数据。
- [ ] **Step 3:** 运行 `go test ./internal/httpapi -run 'TestClearAllImages|Test.*Image' -count=1 -v`。

### Task 5: 添加前端 API 和日志按钮

**Files:** `web-vue/src/api/logs.ts`, `web-vue/src/views/Logs.vue`

- [ ] **Step 1:** 增加 `ClearLogsResult` 和 `logsApi.clearAll()`。
- [ ] **Step 2:** 增加确认、`isClearingLogs`、调用、刷新当前视图/统计和 Toast。
- [ ] **Step 3:** 在日志页顶部 actions 添加“清除全部日志”，清除/加载期间禁用。
- [ ] **Step 4:** 在 `web-vue` 运行 `npm run build`。

### Task 6: 添加前端 API 和图片按钮

**Files:** `web-vue/src/api/gallery.ts`, `web-vue/src/views/Gallery.vue`, `web-vue/src/views/gallery/galleryOperationsRuntime.ts`

- [ ] **Step 1:** 增加 `ClearImagesResult` 和 `galleryApi.clearAll()`。
- [ ] **Step 2:** 增加 `isClearingAll` 与 `handleClearAll`，确认后调用，清空选择、关闭相关状态、刷新图库和存储统计并提示结果。
- [ ] **Step 3:** 在图片页顶部 actions 添加“清除全部图片”，清除/加载期间禁用。
- [ ] **Step 4:** 在 `web-vue` 运行 `npm run build`。

### Task 7: 集成验证与提交

- [ ] **Step 1:** 运行后端定向测试、`go build ./cmd/gptgrok2api`、`go vet ./internal/...`。
- [ ] **Step 2:** 运行前端 `npm run build`。
- [ ] **Step 3:** 运行 `git diff --check` 和 `git status --short`，确认未跟踪的 `参考/`、`服务器.txt`、`review.diff` 不被纳入提交，且没有触碰账号/配置/任务数据。
- [ ] **Step 4:** 提交必要变更：`feat: add one-click cleanup for logs and images`。
