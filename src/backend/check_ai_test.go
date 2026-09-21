package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

// 每条日志必须带时间戳前缀，且多个参数按 fmt.Sprint 语义拼接
func TestRawLogWritesTimestampedLine(t *testing.T) {
	p := setupLog(t, logMaxBytesDefault)
	rawLog("请求URL: ", "https://example.com/v1/chat/completions")

	got := readLog(t, p)
	if !strings.HasPrefix(got, "[") {
		t.Errorf("缺少时间戳前缀: %q", got)
	}
	if !strings.Contains(got, "请求URL: https://example.com/v1/chat/completions") {
		t.Errorf("内容拼接不正确: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("未以换行结尾: %q", got)
	}
}

// 关闭开关时不得创建任何文件
func TestRawLogSkipsWhenDisabled(t *testing.T) {
	p := setupLog(t, logMaxBytesDefault)
	logEnabled = false
	rawLog("不应写入")

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("日志已关闭却仍创建了文件: %v", err)
	}
}

// 核心：超出上限时丢弃最旧记录，保留最新记录，且体积不越界
func TestRawLogTrimsOldestAndKeepsNewest(t *testing.T) {
	const limit = 4096
	p := setupLog(t, limit)
	pad := strings.Repeat("x", 80) // 每行约 110 字节 → 100 行 ≈ 11KB，必然触发裁剪

	for i := 0; i < 100; i++ {
		rawLog("记录编号=", i, " ", pad)
	}

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if fi.Size() > limit {
		t.Fatalf("日志体积 %d 超过上限 %d", fi.Size(), limit)
	}

	got := readLog(t, p)
	if !strings.Contains(got, "记录编号=99") {
		t.Error("最新的记录被误删")
	}
	if strings.Contains(got, "记录编号=0 ") {
		t.Error("最旧的记录未被丢弃")
	}
}

// 裁剪必须对齐行首，不能产生半行；每行都应可独立解读
func TestTrimKeepsLinesIntact(t *testing.T) {
	p := setupLog(t, 2048)
	for i := 0; i < 60; i++ {
		rawLog("行号=", i, " ", strings.Repeat("y", 60))
	}

	got := strings.TrimRight(readLog(t, p), "\n")
	if got == "" {
		t.Fatal("裁剪后日志为空")
	}
	for _, ln := range strings.Split(got, "\n") {
		if !strings.HasPrefix(ln, "[") {
			t.Fatalf("裁剪后出现残行: %q", ln)
		}
	}
}

// 极端情况：单条日志远大于上限时，体积仍不得越界（靠单行 1/5 上限保证）
func TestRawLogCapsSingleHugeLine(t *testing.T) {
	const limit = 4096
	p := setupLog(t, limit)
	rawLog("响应: ", strings.Repeat("z", limit*3))

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if fi.Size() > limit {
		t.Fatalf("单条超长日志使体积越界: %d > %d", fi.Size(), limit)
	}
	if !strings.Contains(readLog(t, p), "单条日志过长已截断") {
		t.Error("超长单条日志未标注截断")
	}
}

// logField：小内容原样返回；超限内容按字符边界截断并标注，且仍是合法 UTF-8
func TestLogFieldTruncation(t *testing.T) {
	if got := logField([]byte("短文本")); got != "短文本" {
		t.Errorf("小内容被改动: %q", got)
	}

	big := []byte(strings.Repeat("中", logFieldMaxBytes/3+100)) // 3 字节/字 → 必超 1MB
	got := logField(big)
	if !strings.Contains(got, "已截断") {
		t.Error("超限内容未标注截断")
	}
	if !utf8.ValidString(got) {
		t.Error("截断结果不是合法 UTF-8")
	}
	if len(got) > logFieldMaxBytes+64 {
		t.Errorf("截断后仍过长: %d 字节", len(got))
	}
}

// cutBytes 不得切断多字节字符
func TestCutBytesRuneBoundary(t *testing.T) {
	s := "中文测试" // 每字 3 字节，共 12 字节
	got, truncated := cutBytes(s, 7)
	if !truncated {
		t.Error("应判定为发生截断")
	}
	if !utf8.ValidString(got) {
		t.Errorf("切断了多字节字符: %q", got)
	}
	if got != "中文" {
		t.Errorf("期望截到「中文」，实际 %q", got)
	}

	if got, truncated := cutBytes(s, 12); truncated || got != s {
		t.Errorf("等长内容不应截断: %q truncated=%v", got, truncated)
	}
}

// 配置解析：[log] 段的新键，以及旧键 debug 的兼容
func TestLoadConfigLogSection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.ini")

	if err := os.WriteFile(p, []byte("[log]\nenabled=false\nmax_size_mb=3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(p)
	if cfg == nil {
		t.Fatal("解析返回 nil")
	}
	if cfg.logEnabled {
		t.Error("enabled=false 未生效")
	}
	if cfg.logMaxMB != 3 {
		t.Errorf("max_size_mb 解析错误: %d", cfg.logMaxMB)
	}

	// 旧键名 debug 仍应被接受
	if err := os.WriteFile(p, []byte("[log]\ndebug=true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !loadConfig(p).logEnabled {
		t.Error("旧键 debug=true 兼容失效")
	}

	// 缺省应为开启且 5MB
	if err := os.WriteFile(p, []byte("[ai]\nmodel=auto\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg = loadConfig(p)
	if !cfg.logEnabled || cfg.logMaxMB != 5 {
		t.Errorf("缺省值错误: enabled=%v maxMB=%d", cfg.logEnabled, cfg.logMaxMB)
	}

	// 非法/非正数应被忽略，不得写入 0 或负数
	if err := os.WriteFile(p, []byte("[log]\nmax_size_mb=abc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(p).logMaxMB; got != 5 {
		t.Errorf("非法 max_size_mb 应回退为 5，实际 %d", got)
	}
	if err := os.WriteFile(p, []byte("[log]\nmax_size_mb=-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(p).logMaxMB; got != 5 {
		t.Errorf("负数 max_size_mb 应回退为 5，实际 %d", got)
	}
}
