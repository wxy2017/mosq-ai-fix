package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 写一份可用的 app.ini（含 [server] port，便于集成测试指定端口）
func writeIni(t *testing.T, path, baseURL, model, key string, port int) {
	t.Helper()
	content := fmt.Sprintf(`[ai]
enabled=true
api_key=%s
model=%s
base_url=%s
timeout=5
max_text=5000

[log]
enabled=true
max_size_mb=5

[server]
port=%d
`, key, model, baseURL, port)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// 核心回归：文件内容一变，current() 必须立刻返回新配置（原来只在进程启动时读一次）
func TestConfigStorePicksUpFileChange(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "app.ini")
	writeIni(t, ini, "https://old.example.com/v1/chat", "model-old", "key-old", 18765)

	store := newConfigStore(ini)
	if got := store.current().baseURL; got != "https://old.example.com/v1/chat" {
		t.Fatalf("初始配置错误: %s", got)
	}

	writeIni(t, ini, "https://new.example.com/v1/chat", "model-new", "key-new", 18765)

	cfg := store.current()
	if cfg.baseURL != "https://new.example.com/v1/chat" {
		t.Errorf("base_url 未热重载，仍是 %s", cfg.baseURL)
	}
	if cfg.model != "model-new" {
		t.Errorf("model 未热重载，仍是 %s", cfg.model)
	}
	if cfg.apiKey != "key-new" {
		t.Errorf("api_key 未热重载，仍是 %s", cfg.apiKey)
	}

	// 内容未变时应返回同一对象（不重复解析）
	if store.current() != cfg {
		t.Error("内容未变时不应重建配置")
	}
}

// 同长度改写也必须被察觉（用内容比对而非 size/mtime，避免漏判）
func TestConfigStoreDetectsSameLengthChange(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "app.ini")
	writeIni(t, ini, "https://aaa.example.com/v1/chat", "m", "k", 18765)
	store := newConfigStore(ini)

	writeIni(t, ini, "https://bbb.example.com/v1/chat", "m", "k", 18765) // 长度完全相同
	if got := store.current().baseURL; got != "https://bbb.example.com/v1/chat" {
		t.Errorf("同长度改写未被察觉，仍是 %s", got)
	}
}

// 配置被临时删掉/占用时，必须沿用上一次的配置，绝不清空
func TestConfigStoreKeepsConfigWhenUnreadable(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "app.ini")
	writeIni(t, ini, "https://keep.example.com/v1/chat", "m", "k", 18765)
	store := newConfigStore(ini)

	if err := os.Remove(ini); err != nil {
		t.Fatal(err)
	}
	cfg := store.current()
	if cfg == nil || cfg.baseURL != "https://keep.example.com/v1/chat" {
		t.Fatalf("文件不可读时配置丢失: %+v", cfg)
	}
}

// 集成测试：常驻服务已启动后改 app.ini，下一次 /polish 实际请求到新地址。
// 这正是用户报的 BUG 场景——旧进程一直在跑（关掉 mosq-ai-fix.exe 也不影响它）。
func TestPolishHandlerReloadsChangedConfig(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "app.ini")

	// 找一个空闲端口写进 [server] port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	writeIni(t, ini, "https://old.example.com/v1/chat", "m-old", "k-old", port)

	// 让日志走内存，避免落盘
	logPath = filepath.Join(dir, "ai_debug.log")
	logEnabled = true
	var logMuLocal sync.Mutex
	var logLines []string
	logSink = func(s string) { logMuLocal.Lock(); logLines = append(logLines, s); logMuLocal.Unlock() }
	defer func() { logSink = nil }()

	// 假传输层：不发真实请求，只记录被请求的 URL
	oldCall := callHTTP
	defer func() { callHTTP = oldCall }()
	var mu sync.Mutex
	var gotURLs []string
	callHTTP = func(cfg *config, req *http.Request) (*http.Response, error) {
		mu.Lock()
		gotURLs = append(gotURLs, req.URL.String())
		mu.Unlock()
		body := `{"choices":[{"message":{"content":"已润色"}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}

	store := newConfigStore(ini)
	go startServer(store)

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	waitServerReady(t, base)

	if got := postPolish(t, base, "第一段文字"); got != "已润色" {
		t.Fatalf("首次调用失败: %q", got)
	}

	// ★ 进程仍在运行，此时修改配置
	writeIni(t, ini, "https://new.example.com/v1/chat", "m-new", "k-new", port)

	if got := postPolish(t, base, "第二段文字"); got != "已润色" {
		t.Fatalf("二次调用失败: %q", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotURLs) != 2 {
		t.Fatalf("期望 2 次真实调用，实际 %d", len(gotURLs))
	}
	if gotURLs[0] != "https://old.example.com/v1/chat" {
		t.Errorf("第一次应请求旧地址，实际 %s", gotURLs[0])
	}
	if gotURLs[1] != "https://new.example.com/v1/chat" {
		t.Fatalf("BUG 复现：改配置后仍在请求旧地址 %s", gotURLs[1])
	}

	logMuLocal.Lock()
	defer logMuLocal.Unlock()
	joined := strings.Join(logLines, "")
	if !strings.Contains(joined, "已热重载配置") {
		t.Error("未记录到热重载日志")
	}
}

func waitServerReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Post(base+"/polish", "text/plain", strings.NewReader(""))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("常驻服务未能在 3 秒内就绪")
}

func postPolish(t *testing.T, base, text string) string {
	t.Helper()
	resp, err := http.Post(base+"/polish", "text/plain", strings.NewReader(text))
	if err != nil {
		t.Fatalf("请求 /polish 失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
