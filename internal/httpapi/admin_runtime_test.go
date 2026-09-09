package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auucoder/gptgrok2api-go/internal/accounts"
	"github.com/auucoder/gptgrok2api-go/internal/config"
)

func adminTestConfig(root string) config.Config {
	return config.Config{
		RootDir: root, DataDir: filepath.Join(root, "data"), StaticDir: filepath.Join(root, "web_dist"),
		ConfigPath: filepath.Join(root, "config.json"), AccountsPath: filepath.Join(root, "data", "accounts.json"),
		AuthKeysPath: filepath.Join(root, "data", "auth_keys.json"), APIKey: "api-secret", AdminKey: "admin-secret", Version: "test",
		ImageDataDir: filepath.Join(root, "data", "files", "images"), VideoDataDir: filepath.Join(root, "data", "files", "videos"),
		OAuthPath: filepath.Join(root, "data", "oauth.json.enc"), QueuePath: filepath.Join(root, "data", "tasks.json"),
		RegisterPath: filepath.Join(root, "data", "register.json"), GrokAccountsPath: filepath.Join(root, "data", "grok_accounts.json"),
	}
}

func adminRequest(handler http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer admin-secret")
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestClearAllLogs(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(cfg.DataDir, "logs.jsonl"):    `{"id":"call-1"}` + "\n" + `{"id":"call-2"}` + "\n",
		filepath.Join(cfg.DataDir, "runtime.log"):   "[INFO] startup\n[ERROR] failed\n",
		filepath.Join(cfg.DataDir, "app.log"):       "app event\n",
		filepath.Join(root, "logs", "runtime.log"):  "[WARNING] old runtime\n",
		filepath.Join(root, "logs", "app.log"):      "old app\n",
		filepath.Join(cfg.DataDir, "accounts.json"): "must remain\n",
	}
	for path, contents := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	response := adminRequest(New(cfg).Handler(), http.MethodPost, "/api/logs/clear", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("clear logs failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		OK         bool  `json:"ok"`
		Files      int   `json:"files"`
		Entries    int   `json:"entries"`
		FreedBytes int64 `json:"freed_bytes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Files != 5 || payload.Entries != 7 || payload.FreedBytes <= 0 {
		t.Fatalf("unexpected clear logs result: %#v", payload)
	}
	for path := range files {
		if filepath.Base(path) == "accounts.json" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("cleared log should remain readable: %s: %v", path, err)
		}
		if len(raw) != 0 {
			t.Fatalf("log was not cleared: %s: %q", path, raw)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "accounts.json")); err != nil {
		t.Fatalf("non-log data was touched: %v", err)
	}
}

func TestClearAllImages(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	nested := filepath.Join(cfg.ImageDataDir, "2026", "09")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(nested, "image.png")
	metaPath := imagePath + ".meta.json"
	nonImagePath := filepath.Join(nested, "keep.txt")
	for path, contents := range map[string]string{
		imagePath:    "png-data",
		metaPath:     "meta-data",
		nonImagePath: "keep-data",
		filepath.Join(cfg.DataDir, "image_tags.json"): `{"2026/09/image.png":["keep"]}`,
		filepath.Join(cfg.VideoDataDir, "keep.mp4"):   "video-data",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	response := adminRequest(New(cfg).Handler(), http.MethodPost, "/api/images/clear", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("clear images failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		OK              bool  `json:"ok"`
		MediaFiles      int   `json:"media_files"`
		MetadataFiles   int   `json:"metadata_files"`
		FreedBytes      int64 `json:"freed_bytes"`
		TagsFileRemoved bool  `json:"tags_file_removed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.MediaFiles != 1 || payload.MetadataFiles != 1 || payload.FreedBytes <= 0 || !payload.TagsFileRemoved {
		t.Fatalf("unexpected clear images result: %#v", payload)
	}
	for _, path := range []string{imagePath, metaPath, filepath.Join(cfg.DataDir, "image_tags.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("file should be removed: %s, err=%v", path, err)
		}
	}
	for _, path := range []string{cfg.ImageDataDir, nonImagePath, filepath.Join(cfg.VideoDataDir, "keep.mp4")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unrelated path should remain: %s: %v", path, err)
		}
	}
}

func TestClearAllImagesRemovesEveryImagePair(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	if err := os.MkdirAll(cfg.ImageDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.png", "second.png"} {
		imagePath := filepath.Join(cfg.ImageDataDir, name)
		if err := os.WriteFile(imagePath, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(imagePath+".meta.json", []byte(`{"source_type":"generated_output"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	response := adminRequest(New(cfg).Handler(), http.MethodPost, "/api/images/clear", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("clear images failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		MediaFiles    int `json:"media_files"`
		MetadataFiles int `json:"metadata_files"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MediaFiles != 2 || payload.MetadataFiles != 2 {
		t.Fatalf("expected both image pairs to be removed, got %#v", payload)
	}
	entries, err := os.ReadDir(cfg.ImageDataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("image directory should be empty, found %d entries", len(entries))
	}
}

func TestClearImageStorageContinuesAfterIndividualDeleteFailure(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked.png")
	removable := filepath.Join(root, "removable.png")
	for path, contents := range map[string]string{
		blocked:   "blocked",
		removable: "removable",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	result := clearImageStorage(root, func(path string) error {
		if path == blocked {
			return os.ErrPermission
		}
		return os.Remove(path)
	})

	if result.mediaFiles != 1 {
		t.Fatalf("expected one image removed, got %d", result.mediaFiles)
	}
	if len(result.failures) != 1 || result.failures[0].Path != blocked {
		t.Fatalf("expected blocked file to be reported, got %#v", result.failures)
	}
	if _, err := os.Stat(removable); !os.IsNotExist(err) {
		t.Fatalf("removable image should be deleted, err=%v", err)
	}
	if _, err := os.Stat(blocked); err != nil {
		t.Fatalf("blocked image should remain for retry, err=%v", err)
	}
}

func TestRuntimeMonitorLifecycle(t *testing.T) {
	monitor := newRuntimeMonitor()
	monitor.start("call-1", "/v1/videos", "video", "hello")
	monitor.update("call-1", "in_progress", 50, "")
	item, ok := monitor.detail("call-1")
	if !ok || item.Progress != 50 || item.Status != "running" {
		t.Fatalf("unexpected active item: %#v %v", item, ok)
	}
	monitor.finish("call-1", "success", "", "", "")
	item, ok = monitor.detail("call-1")
	if !ok || item.Status != "success" || item.Progress != 100 || item.Duration < 0 {
		t.Fatalf("unexpected completed item: %#v %v", item, ok)
	}
}

func TestRuntimeMonitorInitializesAllMetrics(t *testing.T) {
	server := New(adminTestConfig(t.TempDir()))
	server.monitor.start("call-all-metrics", "/v1/images/edits", "gpt-image-2", "test")

	record, ok := server.monitor.detail("call-all-metrics")
	if !ok {
		t.Fatal("monitor record missing")
	}
	for _, key := range monitorMonitorMetricKeys {
		value, exists := record.Metrics[key]
		if !exists {
			t.Fatalf("metric %q was not initialized: %#v", key, record.Metrics)
		}
		if monitorNumber(value) != 0 {
			t.Fatalf("metric %q should start at zero, got %v", key, value)
		}
	}

	server.monitor.finish("call-all-metrics", "success", "gpt-image-2", "test", "")
	summary := mapValue(server.monitorSnapshotWithHistory()["summary"])
	p95 := mapValue(summary["metric_p95"])
	for _, key := range monitorMonitorMetricKeys {
		if value, exists := p95[key]; !exists || monitorNumber(value) != 0 {
			t.Fatalf("summary metric_p95[%q] should be present at zero, got %#v", key, p95)
		}
	}
}

func TestShouldMonitorAllPublicV1PostRoutes(t *testing.T) {
	server := &Server{}
	for _, path := range []string{
		"/v1/chat/completions",
		"/v1/responses",
		"/v1/messages",
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1/videos",
		"/v1/search",
		"/v1/ppt/generations",
		"/v1/psd/generations",
		"/v1/future-endpoint",
		"/upimg/v1/files/image",
	} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		if !server.shouldMonitorRequest(request) {
			t.Errorf("%s should be monitored", path)
		}
	}
	if server.shouldMonitorRequest(httptest.NewRequest(http.MethodGet, "/v1/models", nil)) {
		t.Error("GET /v1/models should not be monitored")
	}
}

func TestRequestMonitorEnrichmentUpdatesLiveEgressAndAccount(t *testing.T) {
	server := &Server{monitor: newRuntimeMonitor()}
	server.monitor.start("call-egress", "/v1/images/generations", "gpt-image-2", "test")
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	request = request.WithContext(context.WithValue(request.Context(), monitorCallIDKey{}, "call-egress"))

	server.enrichMonitorAccount(request, accounts.Account{Pool: "basic", Fields: map[string]any{
		"email":     "image@example.test",
		"proxy_url": "http://proxy-user:proxy-pass@203.0.113.8:8080",
	}})
	server.enrichRequestMonitor(request, map[string]any{
		"egress_label": "http://203.0.113.8:8080",
		"has_proxy":    true,
	})

	record, ok := server.monitor.detail("call-egress")
	if !ok {
		t.Fatal("active monitor record missing")
	}
	if record.AccountEmail != "image@example.test" {
		t.Fatalf("unexpected account email: %q", record.AccountEmail)
	}
	if record.ProxySource != "account" || record.EgressLabel != "http://203.0.113.8:8080" || !record.HasProxy {
		t.Fatalf("unexpected egress metadata: %#v", record)
	}
	if strings.Contains(record.EgressLabel, "proxy-user") || strings.Contains(record.EgressLabel, "proxy-pass") {
		t.Fatalf("proxy credentials leaked into monitor label: %q", record.EgressLabel)
	}
}

func TestAppendCallLogFallsBackToMonitorImageOutputsForB64Response(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	server := New(cfg)
	server.monitor.start("call-b64-image", "/v1/images/edits", "gpt-image-2", "edit")
	server.monitor.enrich("call-b64-image", map[string]any{
		"output_images": []map[string]string{{
			"url":      "/v1/files/image?id=generated-image",
			"filename": "generated-image",
			"width":    "1536",
			"height":   "1024",
		}},
	})
	server.monitor.finish("call-b64-image", "success", "gpt-image-2", "edit", "")
	record, ok := server.monitor.detail("call-b64-image")
	if !ok {
		t.Fatal("completed monitor record missing")
	}

	server.appendCallLog(record, http.StatusOK, map[string]any{
		"size":            "1536x1024",
		"quality":         "high",
		"response_format": "b64_json",
		"requested_n":     1,
	}, []byte("{\"data\":[{\"b64_json\":\"aGVsbG8=\",\"width\":\"1536\",\"height\":\"1024\"}]}"), "")
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "logs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Detail map[string]any `json:"detail"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &entry); err != nil {
		t.Fatal(err)
	}
	outputs, ok := entry.Detail["output_images"].([]any)
	if !ok || len(outputs) != 1 || mapValue(outputs[0])["url"] != "/v1/files/image?id=generated-image" {
		t.Fatalf("expected monitor image output in log, got %#v", entry.Detail["output_images"])
	}
	image := mapValue(outputs[0])
	if image["width"] != "1536" || image["height"] != "1024" {
		t.Fatalf("expected actual output dimensions in log, got %#v", image)
	}
	if entry.Detail["result_data_count"] != float64(1) {
		t.Fatalf("expected actual output count in log, got %#v", entry.Detail["result_data_count"])
	}
	resultImages, ok := entry.Detail["result_images"].([]any)
	if !ok || len(resultImages) != 1 || mapValue(resultImages[0])["width"] != "1536" || mapValue(resultImages[0])["height"] != "1024" {
		t.Fatalf("expected actual output resolution in log, got %#v", entry.Detail["result_images"])
	}
	requestMeta := mapValue(entry.Detail["request_meta"])
	if requestMeta["size"] != "1536x1024" || requestMeta["quality"] != "high" || requestMeta["response_format"] != "b64_json" || requestMeta["requested_n"] != float64(1) {
		t.Fatalf("expected complete image request metadata, got %#v", requestMeta)
	}
}

func TestRequestMonitorDoesNotCountHandlerExecutionAsQueueTime(t *testing.T) {
	server := &Server{monitor: newRuntimeMonitor()}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-2","prompt":"test"}`))
	response := httptest.NewRecorder()
	server.withRequestMonitor(response, request, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	records := server.monitor.completed
	if len(records) != 1 {
		t.Fatalf("expected one completed monitor record, got %d", len(records))
	}
	if queue := monitorNumber(records[0].Metrics["handler_queue_ms"]); queue != 0 {
		t.Fatalf("handler execution was mislabeled as queue time: %vms", queue)
	}
	if records[0].Duration < 20 {
		t.Fatalf("test handler duration was not captured: %dms", records[0].Duration)
	}
}

func TestMonitorSnapshotWithHistorySummary(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	history := map[string]any{
		"type":    "call",
		"id":      "call-2",
		"summary": "history prompt",
		"detail": map[string]any{
			"call_id":     "call-2",
			"endpoint":    "/v1/images/generations",
			"model":       "gpt-image-2",
			"status":      "success",
			"started_at":  now.Add(-2 * time.Second).Format(time.RFC3339),
			"ended_at":    now.Format(time.RFC3339),
			"duration_ms": 2000,
			"monitor": map[string]any{
				"stage": "download",
				"metrics": map[string]any{
					"handler_queue_ms":      100,
					"stream_first_queue_ms": 120,
					"account_wait_ms":       140,
					"egress_wait_ms":        160,
					"download_ms":           180,
					"total_ms":              3000,
				},
				"perf": map[string]any{
					"response_ms": 220,
				},
				"events": []map[string]any{
					{"time": now.Format(time.RFC3339), "event": "download", "label": "下载", "download_ms": 180},
				},
			},
		},
	}
	raw, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "logs.jsonl"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	server := &Server{cfg: cfg, monitor: newRuntimeMonitor()}
	server.monitor.start("call-1", "/v1/chat/completions", "gpt-4o", "hello")
	server.monitor.update("call-1", "running", 35, "")

	snapshot := server.monitorSnapshotWithHistory()
	summary, ok := snapshot["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary missing: %#v", snapshot["summary"])
	}
	if got := monitorNumber(summary["active"]); got != 1 {
		t.Fatalf("unexpected active count: %v", got)
	}
	if got := monitorNumber(summary["completed"]); got != 1 {
		t.Fatalf("unexpected completed count: %v", got)
	}
	if got := monitorNumber(summary["p95_duration_ms"]); got <= 0 {
		t.Fatalf("p95 duration missing: %#v", summary)
	}
	metricP95, ok := summary["metric_p95"].(map[string]any)
	if !ok || monitorNumber(metricP95["handler_queue_ms"]) <= 0 || monitorNumber(metricP95["total_ms"]) <= 0 {
		t.Fatalf("metric p95 missing: %#v", summary["metric_p95"])
	}
	bottleneck, ok := summary["bottleneck"].(map[string]any)
	if !ok || stringValue(bottleneck["label"]) == "" || monitorNumber(bottleneck["value_ms"]) <= 0 {
		t.Fatalf("bottleneck missing: %#v", summary["bottleneck"])
	}
	activeByModel, ok := summary["active_by_model"].(map[string]any)
	if !ok || monitorNumber(activeByModel["gpt-4o"]) != 1 {
		t.Fatalf("active_by_model missing: %#v", summary["active_by_model"])
	}
	activeByStage, ok := summary["active_by_stage"].(map[string]any)
	if !ok || monitorNumber(activeByStage["running"]) != 1 {
		t.Fatalf("active_by_stage missing: %#v", summary["active_by_stage"])
	}
}

func TestRealtimeMonitorEndpointUsesMemoryWindowAndCompactRows(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	history := map[string]any{
		"type":    "call",
		"id":      "historic-call",
		"summary": "must not be loaded by realtime endpoint",
		"detail": map[string]any{
			"call_id":     "historic-call",
			"endpoint":    "/v1/images/generations",
			"model":       "gpt-image-2",
			"status":      "success",
			"started_at":  time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			"ended_at":    time.Now().UTC().Format(time.RFC3339),
			"duration_ms": 60000,
			"monitor": map[string]any{
				"metrics": map[string]any{"total_ms": 60000},
				"events":  []map[string]any{{"event": "historic", "label": "历史"}},
			},
		},
	}
	raw, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "logs.jsonl"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	server := New(cfg)
	server.monitor.start("live-call", "/v1/images/generations", "gpt-image-2", "live prompt")
	server.monitor.enrich("live-call", map[string]any{
		"request_meta": map[string]any{"large": strings.Repeat("x", 4096)},
	})

	response := adminRequest(server.Handler(), http.MethodGet, "/api/monitor/realtime", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected realtime status: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	active, ok := payload["active"].([]any)
	if !ok || len(active) != 1 {
		t.Fatalf("unexpected active rows: %#v", payload["active"])
	}
	activeRow, ok := active[0].(map[string]any)
	if !ok || stringValue(activeRow["call_id"]) != "live-call" {
		t.Fatalf("unexpected active row: %#v", active[0])
	}
	for _, key := range []string{"events", "request_meta"} {
		if _, exists := activeRow[key]; exists {
			t.Fatalf("compact active row contains %q: %#v", key, activeRow)
		}
	}
	metrics, ok := activeRow["metrics"].(map[string]any)
	if !ok || metrics == nil {
		t.Fatalf("active row lost compact timing metrics: %#v", activeRow)
	}
	recent, ok := payload["recent"].([]any)
	if !ok || len(recent) != 0 {
		t.Fatalf("realtime endpoint loaded historical rows: %#v", payload["recent"])
	}
	events, ok := payload["events"].([]any)
	if !ok || len(events) == 0 {
		t.Fatalf("live monitor events missing: %#v", payload["events"])
	}
}

func TestDashboardAccountAndLogSummary(t *testing.T) {
	accountStats := dashboardAccountStats([]map[string]any{
		{"access_token": "header.payload.signature", "status": "正常", "enabled": true, "quota": 25, "type": "plus", "success": 3},
		{"access_token": "second.jwt.token", "status": "限流", "enabled": true, "quota": 10, "type": "free", "fail": 1},
		{"access_token": "grok-disabled", "status": "禁用", "enabled": false, "source_type": "grok"},
		{"access_token": "grok-abnormal", "status": "异常", "enabled": true, "source_type": "grok", "invalid_count": 1},
	})
	if intValue(accountStats["total"]) != 4 || intValue(accountStats["active"]) != 1 || intValue(accountStats["limited"]) != 1 {
		t.Fatalf("unexpected account totals: %#v", accountStats)
	}
	if intValue(accountStats["abnormal"]) != 1 || intValue(accountStats["disabled"]) != 1 || intValue(accountStats["total_quota"]) != 25 {
		t.Fatalf("unexpected account categories: %#v", accountStats)
	}
	providers := mapValue(accountStats["providers"])
	if intValue(mapValue(providers["gpt"])["total"]) != 2 || intValue(mapValue(providers["grok"])["total"]) != 2 {
		t.Fatalf("unexpected provider totals: %#v", providers)
	}

	now := time.Date(2026, 8, 30, 13, 45, 0, 0, time.FixedZone("CST", 8*60*60))
	callLog := func(id, status string, statusCode int, startedAt time.Time, duration int) map[string]any {
		return map[string]any{
			"id": id, "type": "call", "time": startedAt.Format(time.RFC3339), "summary": id,
			"detail": map[string]any{
				"status": status, "status_code": statusCode, "started_at": startedAt.Format(time.RFC3339),
				"endpoint": "/v1/images/generations", "model": "gpt-image-2", "duration_ms": duration,
				"monitor": map[string]any{"metrics": map[string]any{"http_ttfb_ms": 500, "total_ms": duration}},
			},
		}
	}
	logs := []map[string]any{
		callLog("success", "success", 200, now.Add(-30*time.Minute), 2000),
		callLog("limited", "failed", 429, now.Add(-10*time.Minute), 3000),
		callLog("old", "success", 200, now.Add(-48*time.Hour), 1000),
	}
	summary := dashboardLogSummary(logs, "24h", now)
	if intValue(summary["total"]) != 2 || intValue(summary["success"]) != 1 || intValue(summary["failed"]) != 1 {
		t.Fatalf("unexpected log totals: %#v", summary)
	}
	trend := mapValue(summary["trend"])
	labels, ok := trend["labels"].([]string)
	if !ok || len(labels) != 24 {
		t.Fatalf("unexpected trend labels: %#v", trend["labels"])
	}
	modelSeries, ok := trend["model_requests"].(map[string][]int)
	if !ok || len(modelSeries["gpt-image-2"]) != 24 || modelSeries["gpt-image-2"][23] != 2 {
		t.Fatalf("unexpected model series: %#v", trend["model_requests"])
	}
	rateLimited := trend["rate_limited_requests"].([]int)
	if rateLimited[23] != 1 {
		t.Fatalf("rate limited request missing: %#v", rateLimited)
	}
}

func TestDashboardRouteDisablesCaching(t *testing.T) {
	server := New(adminTestConfig(t.TempDir()))
	response := adminRequest(server.Handler(), http.MethodGet, "/api/dashboard?time_range=24h", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected dashboard status: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("dashboard response can be cached: %#v", response.Header())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["generated_at"] == nil || mapValue(payload["accounts"])["providers"] == nil || mapValue(payload["logs"])["trend"] == nil {
		t.Fatalf("dashboard payload is incomplete: %#v", payload)
	}
}

func TestRunImageTaskRecordsRealtimeMonitorAndCallLog(t *testing.T) {
	root := t.TempDir()
	server := New(adminTestConfig(root))
	task := &imageTaskState{
		ID:     "task-monitor-regression",
		Status: "queued",
		Mode:   "generate",
		Model:  "__monitor_invalid_model__",
		Prompt: "monitor regression",
		N:      1,
		Size:   "1024x1024",
	}

	server.runImageTask(task, "Bearer admin-secret", "")

	if task.Status != "error" {
		t.Fatalf("expected task error, got %q (%s)", task.Status, task.Error)
	}
	record, ok := server.monitor.detail(task.ID)
	if !ok {
		t.Fatal("async image task was not recorded by realtime monitor")
	}
	if record.Endpoint != "/v1/images/generations" || record.Status != "failed" {
		t.Fatalf("unexpected async monitor record: %#v", record)
	}
	if record.Error == "" || record.Duration < 0 {
		t.Fatalf("monitor failure details missing: %#v", record)
	}

	snapshot := server.monitorSnapshotWithHistory()
	summary := mapValue(snapshot["summary"])
	if intValue(summary["completed"]) != 1 || intValue(summary["failed"]) != 1 {
		t.Fatalf("async task missing from realtime summary: %#v", summary)
	}

	raw, err := os.ReadFile(filepath.Join(server.cfg.DataDir, "logs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), task.ID) {
		t.Fatalf("async task was not persisted to call log: %s", raw)
	}
}

func TestRunImageTaskUsesEditEndpointForAsyncEdits(t *testing.T) {
	root := t.TempDir()
	server := New(adminTestConfig(root))
	task := &imageTaskState{
		ID:         "task-edit-monitor-regression",
		Status:     "queued",
		Mode:       "edit",
		Model:      "__monitor_invalid_model__",
		Prompt:     "edit monitor regression",
		N:          1,
		Size:       "1024x1024",
		Images:     [][]byte{[]byte("not-a-real-image")},
		ImageNames: []string{"input.png"},
	}

	server.runImageTask(task, "Bearer admin-secret", "")

	record, ok := server.monitor.detail(task.ID)
	if !ok || record.Endpoint != "/v1/images/edits" || record.Status != "failed" {
		t.Fatalf("unexpected async edit monitor record: %#v %v", record, ok)
	}
}

func TestRequestMonitorWritesMultipartCallLog(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	server := &Server{cfg: cfg, monitor: newRuntimeMonitor()}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-image-2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("prompt", "make a blue square"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.withRequestMonitor(response, request, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))

	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "logs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	if len(lines) != 1 {
		t.Fatalf("unexpected log line count: %d", len(lines))
	}
	var item map[string]any
	if err := json.Unmarshal(lines[0], &item); err != nil {
		t.Fatal(err)
	}
	detail := mapValue(item["detail"])
	if stringValue(detail["model"]) != "gpt-image-2" {
		t.Fatalf("model not logged: %#v", detail)
	}
	if !strings.Contains(stringValue(detail["request_text"]), "blue square") {
		t.Fatalf("prompt not logged: %#v", detail)
	}
	if stringValue(mapValue(detail["request_shape"])["content_type"]) != "multipart/form-data" {
		t.Fatalf("request shape not logged: %#v", detail)
	}
	monitor := mapValue(detail["monitor"])
	if len(anyList(monitor["events"])) < 2 {
		t.Fatalf("monitor events missing: %#v", detail)
	}
}

func TestResponseImageOutputsIgnoresMalformedURLs(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"content":"![image](http://%zz/v1/files/image?id=bad)"}}]}`)
	outputs := responseImageOutputs(raw)
	if len(outputs) != 0 {
		t.Fatalf("malformed image URL should be ignored: %#v", outputs)
	}

	raw = []byte(`{"choices":[{"message":{"content":"![image](http://127.0.0.1:8000/v1/files/image?id=ok)"}}]}`)
	outputs = responseImageOutputs(raw)
	if len(outputs) != 1 || outputs[0]["filename"] != "ok" {
		t.Fatalf("valid image URL was not recorded: %#v", outputs)
	}
}

func TestCleanupExpiredImagesUsesRetentionDays(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	cfg.ImageRetentionDays = 1
	server := &Server{cfg: cfg}
	if err := os.MkdirAll(cfg.ImageDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(cfg.ImageDataDir, "old.png")
	newPath := filepath.Join(cfg.ImageDataDir, "new.png")
	metaPath := filepath.Join(cfg.ImageDataDir, "old.png.meta.json")
	for _, path := range []string{oldPath, newPath, metaPath} {
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(metaPath, old, old); err != nil {
		t.Fatal(err)
	}
	removed, bytes := server.cleanupExpiredImages()
	if removed != 2 || bytes != 8 {
		t.Fatalf("unexpected cleanup result: removed=%d bytes=%d", removed, bytes)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old image was not removed: %v", err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("old metadata was not removed: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new image should remain: %v", err)
	}
}

func TestAdminImagesTagsAndBackup(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	if err := os.MkdirAll(cfg.ImageDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.ImageDataDir, "image-one.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg).Handler()

	list := adminRequest(handler, http.MethodGet, "/api/images", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "image-one.png") {
		t.Fatalf("unexpected image list: %d %s", list.Code, list.Body.String())
	}

	tagsBody := strings.NewReader(`{"path":"image-one.png","tags":["work","work"]}`)
	tags := adminRequest(handler, http.MethodPost, "/api/images/tags", tagsBody)
	if tags.Code != http.StatusOK || !strings.Contains(tags.Body.String(), "work") {
		t.Fatalf("unexpected tags response: %d %s", tags.Code, tags.Body.String())
	}
	allTags := adminRequest(handler, http.MethodGet, "/api/images/tags", nil)
	if allTags.Code != http.StatusOK || !strings.Contains(allTags.Body.String(), "work") {
		t.Fatalf("unexpected tag list: %d %s", allTags.Code, allTags.Body.String())
	}

	backup := adminRequest(handler, http.MethodPost, "/api/backups/run", nil)
	if backup.Code != http.StatusOK {
		t.Fatalf("backup failed: %d %s", backup.Code, backup.Body.String())
	}
	var backupResponse map[string]any
	if err := json.Unmarshal(backup.Body.Bytes(), &backupResponse); err != nil {
		t.Fatal(err)
	}
	result, _ := backupResponse["result"].(map[string]any)
	key, _ := result["key"].(string)
	if key == "" {
		t.Fatalf("backup key missing: %#v", backupResponse)
	}
	archivePath := filepath.Join(cfg.DataDir, "backups", key)
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = archive.Close()

	delete := adminRequest(handler, http.MethodPost, "/api/images/delete", strings.NewReader(`{"paths":["image-one.png"]}`))
	if delete.Code != http.StatusOK || strings.Contains(delete.Body.String(), `"removed":0`) {
		t.Fatalf("image deletion failed: %d %s", delete.Code, delete.Body.String())
	}
	if _, err := os.Stat(filepath.Join(cfg.ImageDataDir, "image-one.png")); !os.IsNotExist(err) {
		t.Fatalf("image was not deleted: %v", err)
	}
}

func TestRegistrationManagementEndpoints(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	if err := os.MkdirAll(filepath.Dir(cfg.GrokAccountsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	account := `[{"id":"grok-one","email":"alice@example.com","password":"secret","sso":"sso-token","status":"active","source_type":"protocol"}]`
	if err := os.WriteFile(cfg.GrokAccountsPath, []byte(account), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg).Handler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/register", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("register endpoint should require admin key: %d", unauthorized.Code)
	}

	configResponse := adminRequest(handler, http.MethodGet, "/api/register", nil)
	if configResponse.Code != http.StatusOK || !strings.Contains(configResponse.Body.String(), `"register"`) {
		t.Fatalf("unexpected register config: %d %s", configResponse.Code, configResponse.Body.String())
	}
	startResponse := adminRequest(handler, http.MethodPost, "/api/register/start", nil)
	if startResponse.Code != http.StatusServiceUnavailable || !strings.Contains(startResponse.Body.String(), `"ready":false`) {
		t.Fatalf("register start should report unavailable executor: %d %s", startResponse.Code, startResponse.Body.String())
	}
	runtimeResponse := adminRequest(handler, http.MethodGet, "/api/register/runtime", nil)
	if runtimeResponse.Code != http.StatusOK || !strings.Contains(runtimeResponse.Body.String(), `"ready":false`) {
		t.Fatalf("unexpected registration runtime status: %d %s", runtimeResponse.Code, runtimeResponse.Body.String())
	}
	listResponse := adminRequest(handler, http.MethodGet, "/api/register/grok/accounts?page_size=10", nil)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), "al***e@example.com") {
		t.Fatalf("register list failed: %d %s", listResponse.Code, listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), "sso-token") || strings.Contains(listResponse.Body.String(), `"password":"secret"`) {
		t.Fatalf("register list leaked credentials: %s", listResponse.Body.String())
	}

	authorizeResponse := adminRequest(handler, http.MethodPost, "/api/register/grok/accounts/oauth/authorize", strings.NewReader(`{"ids":["grok-one"]}`))
	if authorizeResponse.Code != http.StatusOK || !strings.Contains(authorizeResponse.Body.String(), `"status":"queued"`) {
		t.Fatalf("OAuth authorization was not queued: %d %s", authorizeResponse.Code, authorizeResponse.Body.String())
	}
	credentialsResponse := adminRequest(handler, http.MethodGet, "/api/register/grok/accounts/grok-one/credentials", nil)
	if credentialsResponse.Code != http.StatusOK || !strings.Contains(credentialsResponse.Body.String(), "secret") {
		t.Fatalf("credentials endpoint failed: %d %s", credentialsResponse.Code, credentialsResponse.Body.String())
	}
	ssoResponse := adminRequest(handler, http.MethodGet, "/api/register/grok/accounts/export-sso", nil)
	if ssoResponse.Code != http.StatusOK || !strings.Contains(ssoResponse.Body.String(), "sso-token") {
		t.Fatalf("SSO export failed: %d %s", ssoResponse.Code, ssoResponse.Body.String())
	}
	disableResponse := adminRequest(handler, http.MethodPost, "/api/register/grok/accounts/runtime/disabled", strings.NewReader(`{"ids":["grok-one"],"disabled":true}`))
	if disableResponse.Code != http.StatusOK || !strings.Contains(disableResponse.Body.String(), `"ok":1`) {
		t.Fatalf("disable endpoint failed: %d %s", disableResponse.Code, disableResponse.Body.String())
	}
}

func TestImportedAbnormalAccountCleanup(t *testing.T) {
	root := t.TempDir()
	cfg := adminTestConfig(root)
	if err := os.MkdirAll(filepath.Dir(cfg.AccountsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	accounts := `[
  {"access_token":"abnormal-token","status":"异常","enabled":true},
  {"access_token":"normal-token","status":"正常","enabled":true}
]`
	if err := os.WriteFile(cfg.AccountsPath, []byte(accounts), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(cfg).Handler()

	preview := adminRequest(handler, http.MethodPost, "/api/accounts/import-cleanup", strings.NewReader(`{"access_tokens":["abnormal-token","normal-token","abnormal-token"],"remove":false}`))
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"checked":2`) || !strings.Contains(preview.Body.String(), `"abnormal":1`) || !strings.Contains(preview.Body.String(), `"removed":0`) {
		t.Fatalf("unexpected cleanup preview: %d %s", preview.Code, preview.Body.String())
	}

	removed := adminRequest(handler, http.MethodPost, "/api/accounts/import-cleanup", strings.NewReader(`{"access_tokens":["abnormal-token","normal-token"],"remove":true}`))
	if removed.Code != http.StatusOK || !strings.Contains(removed.Body.String(), `"abnormal":1`) || !strings.Contains(removed.Body.String(), `"removed":1`) {
		t.Fatalf("unexpected cleanup result: %d %s", removed.Code, removed.Body.String())
	}
	items, err := New(cfg).store.AccountList()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || stringValue(items[0]["access_token"]) != "normal-token" {
		t.Fatalf("unexpected accounts after cleanup: %#v", items)
	}
}
