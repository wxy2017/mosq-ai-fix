package main

// 测试基础设施（helpers）。
//
// 这里只保留"写测试要用的脚手架"：临时配置、假传输层、起测试服务、
// 日志捕获、HTTP 断言辅助等。**原有的 30 个用例已按用户要求删除**，
// 本文件当前不含任何 TestXxx，go test 会报 [no tests to run] —— 属预期。
// 需要加回用例时，直接在本目录新建 xxx_test.go 使用下面的辅助函数即可。

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

// setupLog 为用例准备独立的日志环境，避免相互污染全局状态。
func setupLog(t *testing.T, maxBytes int64) string {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "ai_debug.log")
	logEnabled = true
	logMaxBytes = maxBytes
	return logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	return string(b)
}

// 写一份可用的 app.ini（含 [server] port，便于集成测试指定端口）。
// 顺带把 baseDir（部署根）指到该 ini 所在目录：这样 newConfigStore 解析默认日志
// 目录时会落进 t.TempDir()，不会在源码目录里冒出 config\ai_debug.log。
func writeIni(t *testing.T, path, baseURL, model, key string, port int) {
	t.Helper()
	baseDir = filepath.Dir(path)
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
	return postText(t, base+"/polish", text)
}

// postText 向任意文本端点 POST 并返回响应体
func postText(t *testing.T, endpointURL, text string) string {
	t.Helper()
	body, _ := postTextHdr(t, endpointURL, text)
	return body
}

// postTextHdr 同 postText，但额外返回响应头（用于断言 X-Model）
func postTextHdr(t *testing.T, endpointURL, text string) (string, http.Header) {
	t.Helper()
	resp, err := http.Post(endpointURL, "text/plain", strings.NewReader(text))
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", endpointURL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.Header
}

// fakeModel 假响应体里回显的模型名（模拟"上游实际使用的模型"）
const fakeModel = "fake-model-1"

// 捕获日志（供断言用）；每个测试开始时清空
var testLog struct {
	mu    sync.Mutex
	lines []string
}

// slowBody 让"读响应体"这一步人为变慢，用来模拟真实大模型的行为：
// 先回响应头（TTFB 很小），再花几秒把正文生成完。
type slowBody struct {
	io.ReadCloser
	delay time.Duration
	done  bool
}

// fakeAPI 装好一个假的大模型传输层，返回：读到的请求 URL、读到的请求体（JSON）。
// 用闭包持有的指针让调用方在外层读取结果。不改真实网络、不消耗额度。
func fakeAPI(t *testing.T, reply string) (*string, *string) {
	t.Helper()
	old := callHTTP
	t.Cleanup(func() { callHTTP = old })

	gotURL, gotBody := new(string), new(string)
	callHTTP = func(cfg *config, req *http.Request) (*http.Response, error) {
		*gotURL = req.URL.String()
		b, _ := io.ReadAll(req.Body)
		*gotBody = string(b)
		body := fmt.Sprintf(`{"model":%q,"choices":[{"message":{"content":%q}}]}`, fakeModel, reply)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	return gotURL, gotBody
}

func resetTestLog() {
	testLog.mu.Lock()
	testLog.lines = nil
	testLog.mu.Unlock()
}

func testLogText() string {
	testLog.mu.Lock()
	defer testLog.mu.Unlock()
	return strings.Join(testLog.lines, "")
}

// 起一个常驻服务（假传输层已就位），返回 base URL
func startTestServer(t *testing.T, ini string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	writeIni(t, ini, "https://api.example.com/v1/chat/completions", "m", "k", port)

	resetTestLog()
	logPath = filepath.Join(filepath.Dir(ini), "ai_debug.log")
	logEnabled = true
	logSink = func(s string) {
		testLog.mu.Lock()
		testLog.lines = append(testLog.lines, s)
		testLog.mu.Unlock()
	}
	t.Cleanup(func() { logSink = nil })

	store := newConfigStore(ini)
	go startServer(store)

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	waitServerReady(t, base)
	return base
}

func (b *slowBody) Read(p []byte) (int, error) {
	if !b.done {
		b.done = true
		time.Sleep(b.delay)
	}
	return b.ReadCloser.Read(p)
}

// writeIniLogDir 写一份只关心 [log] 段的 app.ini，并把 baseDir 指向它所在目录。
// logDir 传 "" 表示"不配置 [log] dir"（走默认目录）。
func writeIniLogDir(t *testing.T, dir, logDir string) string {
	t.Helper()
	baseDir = dir
	ini := filepath.Join(dir, "app.ini")
	logSection := "[log]\nenabled=true\nmax_size_mb=5\n"
	if logDir != "" {
		logSection = "[log]\nenabled=true\nmax_size_mb=5\ndir=" + logDir + "\n"
	}
	content := "[ai]\nenabled=true\napi_key=k\nmodel=m\nbase_url=https://x/v1/chat\n\n" + logSection
	if err := os.WriteFile(ini, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return ini
}
