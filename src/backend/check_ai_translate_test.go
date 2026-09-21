package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
		body := fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, reply)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	return gotURL, gotBody
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

	logPath = filepath.Join(filepath.Dir(ini), "ai_debug.log")
	logEnabled = true
	logSink = func(string) {}
	t.Cleanup(func() { logSink = nil })

	store := newConfigStore(ini)
	go startServer(store)

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	waitServerReady(t, base)
	return base
}

// 核心：/translate 必须走"翻译提示词"，且与 /polish 的提示词互不串台
func TestTranslateEndpointUsesTranslatePrompt(t *testing.T) {
	dir := t.TempDir()
	gotURL, gotBody := fakeAPI(t, "会议已推迟到下周一。")
	base := startTestServer(t, filepath.Join(dir, "app.ini"))

	got := postText(t, base+"/translate", "The meeting has been postponed to next Monday.")
	if got != "会议已推迟到下周一。" {
		t.Fatalf("译文不正确: %q", got)
	}
	if *gotURL != "https://api.example.com/v1/chat/completions" {
		t.Errorf("未使用配置里的 base_url: %s", *gotURL)
	}
	if !strings.Contains(*gotBody, "专业的中文翻译") {
		t.Error("请求体没有使用翻译提示词")
	}
	if strings.Contains(*gotBody, "资深的中文编辑") {
		t.Error("请求体混入了润色提示词（两个端点串台了）")
	}
	if !strings.Contains(*gotBody, "The meeting has been postponed") {
		t.Error("请求体没有带上待翻译的原文")
	}
}

// /polish 必须仍然走润色提示词（防止重构后两个端点接错）
func TestPolishEndpointKeepsPolishPrompt(t *testing.T) {
	dir := t.TempDir()
	_, gotBody := fakeAPI(t, "我今天真的很想去。")
	base := startTestServer(t, filepath.Join(dir, "app.ini"))

	if got := postText(t, base+"/polish", "我今天挺想去的。"); got != "我今天真的很想去。" {
		t.Fatalf("润色结果不正确: %q", got)
	}
	if !strings.Contains(*gotBody, "资深的中文编辑") {
		t.Error("请求体没有使用润色提示词")
	}
	if strings.Contains(*gotBody, "专业的中文翻译") {
		t.Error("请求体混入了翻译提示词")
	}
}

// 空文本必须直接返回空串且不触发大模型调用 —— 前端正是用这一点做端点连通性探针
func TestEmptyTextSkipsAPICallOnBothEndpoints(t *testing.T) {
	dir := t.TempDir()
	called := false
	fakeAPI(t, "不应被调用")
	// 包一层，记录是否真的发出过请求
	inner := callHTTP
	callHTTP = func(cfg *config, req *http.Request) (*http.Response, error) {
		called = true
		return inner(cfg, req)
	}
	base := startTestServer(t, filepath.Join(dir, "app.ini"))

	for _, ep := range []string{"/polish", "/translate"} {
		if got := postText(t, base+ep, ""); got != "" {
			t.Errorf("%s 空文本应返回空串，实际 %q", ep, got)
		}
	}
	if called {
		t.Error("空文本不应触发大模型调用（探测会白耗额度）")
	}
}

// 未配置 api_key 时两个端点都应返回 __NO_KEY__
func TestNoKeyReturnsMarkerOnBothEndpoints(t *testing.T) {
	dir := t.TempDir()
	fakeAPI(t, "不应被调用")
	ini := filepath.Join(dir, "app.ini")

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	writeIni(t, ini, "https://api.example.com/v1/chat", "m", "", port) // api_key 留空

	logPath = filepath.Join(dir, "ai_debug.log")
	logEnabled = true
	logSink = func(string) {}
	t.Cleanup(func() { logSink = nil })

	store := newConfigStore(ini)
	go startServer(store)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	waitServerReady(t, base)

	for _, ep := range []string{"/polish", "/translate"} {
		if got := postText(t, base+ep, "hello"); got != "__NO_KEY__" {
			t.Errorf("%s 应返回 __NO_KEY__，实际 %q", ep, got)
		}
	}
}

// 翻译提示词本身要满足关键约束（防呆：别把"已是中文原样返回"这类规则改丢）
func TestTranslatePromptGuards(t *testing.T) {
	for _, want := range []string{"简体中文", "已是中文", "不要改写", "保留格式", "只输出译文"} {
		if !strings.Contains(translatePrompt, want) {
			t.Errorf("翻译提示词缺少关键约束: %q", want)
		}
	}
}
