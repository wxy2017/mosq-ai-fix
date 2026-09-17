// check_ai.go — 文本润色工具（Go 版）· 云端大模型
//
// 用法: check_ai.exe -polish <input.txt> <output.txt>
//   output.txt 直接写润色后的整段文本
//   特殊标记: __NO_KEY__ 未配置Key | __ERROR__xxx 失败
//
// 常驻服务模式: check_ai.exe -server
//   监听 127.0.0.1:<[server] port>，提供 POST /polish（前端热键调用的唯一入口）
//
// v5.2 起移除错字检查（F8）功能，本程序仅负责语句润色（F9）。
// 部署布局（v5.3）：本 exe 位于 <部署根>\runtime\，其上一级即部署根
//   - 读取 <部署根>/config/app.ini（[ai]/[log]/[server] 段）
//   - 调用云端 API（润色提示词）
//   - debug=true 时写 <部署根>/config/ai_debug.log
//   - 缓存写 <部署根>/.cache
//
// 编译: 推荐 build\build.ps1；或 cd src && go build -o ../dist/runtime/check_ai.exe ./backend
// 本程序仅使用 Go 标准库，无第三方依赖，免安装、免 Python 环境。

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// polishPrompt 增强润色模式：参考WorkBuddy增强提示词，使润色更专业、得体
const polishPrompt = `你是一个资深的中文编辑和写作老师，专长于润色各种场景的中文文本，使其更加得体、准确、流畅且富有感染力，同时严格保留原意和关键信息。
任务：对用户提供的中文段落进行润色改写。
核心原则：
1. 绝对保持原意不变：不添加、删除或修改任何事实信息、数据、专有名词（人名、地名、机构名）、英文术语、数字、日期时间等客观内容。
2. 仅改进语言表达：修正语法错误、用词不当、搭配错误；改善语序不通顺、口语化表达、重复冗余、语气生硬；使句子更加书面化、连贯、有逻辑性。
3. 提升可读性和雅度：适当使用书面化表达，但避免过于晦涩或文雅过度；保持原文的基本语气和风格（如正式报告保持正式，轻松聊天保持轻松自然）。
4. 段落结构保持：不改变段落划分和整体逻辑顺序；如有列表或编号，保持其格式。
5. 处理特殊内容：对代码、公式、表格等非自然语言内容保持原样；对引用内容保持原样但可适当调整前后连接句使其流畅。
输出要求：
- 只输出润色后的完整文本，不要添加任何解释、评论或标记。
- 不要使用代码块、引号或任何额外符号包裹输出。
- 如果原文本身已经非常得体通顺，则直接返回原文。
- 保持输出与输入的换行格式一致。
示例：
输入：他很快的跑完了比赛，得了第一名。
输出：他迅速完成了比赛，获得第一名。
输入：由于天气不好，所以我们决定取消今天的活动。
输出：鉴于天气原因，我们决定取消今天的活动。`

var logPath string // ai_debug.log 绝对路径（main 中初始化）

// ---------- 配置 ----------

type config struct {
	enabled    bool
	apiKey     string
	model      string
	baseURL    string
	timeout    int
	maxText    int
	debug      bool
	serverPort string
}

// loadConfig 解析 <工具根>/config/typo_config.ini（兼容 ; 和 # 注释）
func loadConfig(path string) *config {
	cfg := &config{
		model:      "glm-4-flash",
		baseURL:    "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		timeout:    8,
		maxText:    500,
		serverPort: "18765",
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	section := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		// 去掉行尾注释（"值 ; 注释" 形式）
		if i := strings.Index(val, " ;"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		switch section {
		case "ai":
			switch key {
			case "enabled":
				cfg.enabled = strings.EqualFold(val, "true")
			case "api_key":
				cfg.apiKey = val
			case "model":
				cfg.model = val
			case "base_url":
				cfg.baseURL = val
			case "timeout":
				if n, err := strconv.Atoi(val); err == nil {
					cfg.timeout = n
				}
			case "max_text":
				if n, err := strconv.Atoi(val); err == nil {
					cfg.maxText = n
				}
			}
		case "log":
			switch key {
			case "debug":
				cfg.debug = strings.EqualFold(val, "true")
			}
		case "server":
			switch key {
			case "port":
				cfg.serverPort = val
			}
		}
	}
	return cfg
}

// ---------- 调试日志 ----------

// rawLog 无条件写一行日志（不依赖配置是否加载成功）
func rawLog(msg string) {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(f, "[%s] %s\n", ts, msg)
}

// debugLog 仅在 debug=true 时写日志
func debugLog(cfg *config, msg string) {
	if cfg == nil || !cfg.debug {
		return
	}
	rawLog(msg)
}

// ---------- 智谱 API ----------

func callAPI(cfg *config, prompt, text string) (string, error) {
	payload := map[string]any{
		"model": cfg.model,
		"messages": []map[string]string{
			{"role": "system", "content": prompt},
			{"role": "user", "content": text},
		},
		"temperature": 0.2,
		"stream":      false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", cfg.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: time.Duration(cfg.timeout) * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 90 * time.Second,
			},
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	var data struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return "", err
	}
	if len(data.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return data.Choices[0].Message.Content, nil
}

// ---------- 性能优化：缓存 / 分块 / 常驻服务 ----------

// promptVersion 缓存失效标记：polishPrompt 变更时改此值（v5.2 起仅覆盖润色）
const promptVersion = "v5.2"

var httpClient *http.Client
var cacheDir string

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func cachePath(key string) string {
	return filepath.Join(cacheDir, sha256hex(key)+".json")
}

func cacheGet(key string) ([]byte, bool) {
	data, err := os.ReadFile(cachePath(key))
	if err != nil {
		return nil, false
	}
	return data, true
}

func cachePut(key string, data []byte) {
	_ = os.WriteFile(cachePath(key), data, 0644)
}

// corePolish 单段润色（含缓存）
func corePolish(cfg *config, text string) (string, error) {
	key := "polish|" + promptVersion + "|" + cfg.model + "|" + cfg.baseURL + "|" + text
	if data, ok := cacheGet(key); ok {
		return string(data), nil
	}
	content, err := callAPI(cfg, polishPrompt, text)
	if err != nil {
		return "", err
	}
	cachePut(key, []byte(content))
	return content, nil
}

// processPolish 对外润色入口：超长分块拼接
func processPolish(cfg *config, text string) (string, error) {
	runes := []rune(text)
	if len(runes) <= cfg.maxText {
		return corePolish(cfg, text)
	}
	overlap := 30
	var sb strings.Builder
	for i := 0; i < len(runes); i += (cfg.maxText - overlap) {
		end := i + cfg.maxText
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		content, err := corePolish(cfg, chunk)
		if err != nil {
			return "", err
		}
		sb.WriteString(cleanPolish(content))
		if end == len(runes) {
			break
		}
	}
	return sb.String(), nil
}

// startServer 常驻本地服务：复用连接池，提供 /polish 端点
func startServer(cfg *config) {
	httpClient = &http.Client{
		Timeout: time.Duration(cfg.timeout) * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:    10,
			IdleConnTimeout: 90 * time.Second,
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/polish", func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil || cfg.apiKey == "" {
			io.WriteString(w, "__NO_KEY__")
			return
		}
		body, _ := io.ReadAll(r.Body)
		text := strings.TrimSpace(string(body))
		if text == "" {
			io.WriteString(w, "")
			return
		}
		content, err := processPolish(cfg, text)
		if err != nil {
			io.WriteString(w, "__ERROR__"+err.Error())
			return
		}
		if strings.TrimSpace(content) == "" {
			io.WriteString(w, "__ERROR__空响应")
			return
		}
		io.WriteString(w, content)
	})
	addr := "127.0.0.1:" + cfg.serverPort
	_ = http.ListenAndServe(addr, mux)
}

// ---------- 结果处理 ----------

// cleanPolish 清理润色输出：去掉代码块包裹和模型误加的首尾引号
func cleanPolish(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) > 2 {
			s = strings.Join(lines[1:len(lines)-1], "\n")
		} else {
			s = ""
		}
	}
	s = strings.TrimSpace(s)
	if len([]rune(s)) >= 2 {
		r := []rune(s)
		first, last := r[0], r[len(r)-1]
		if (first == '"' && last == '"') || (first == '“' && last == '”') ||
			(first == '「' && last == '」') || (first == '\'' && last == '\'') {
			s = strings.TrimSpace(string(r[1 : len(r)-1]))
		}
	}
	return s
}

// ---------- 主流程 ----------

func main() {
	// 路径基准（v5.3 部署布局）：exe 位于 <部署根>/runtime/，
	// 配置与日志位于 <部署根>/config/ —— 从 exe 向上**一级**即部署根
	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	}
	exeDir := filepath.Dir(exePath)  // .../dist/runtime
	rootDir := filepath.Dir(exeDir)  // 部署根（dist/）
	iniPath := filepath.Join(rootDir, "config", "app.ini")
	logPath = filepath.Join(rootDir, "config", "ai_debug.log")

	cfg := loadConfig(iniPath)
	if cfg == nil {
		cfg = &config{serverPort: "18765"}
	}
	cacheDir = filepath.Join(rootDir, ".cache")
	_ = os.MkdirAll(cacheDir, 0755)

	// 常驻服务模式：启动本地 HTTP 服务后退出（由前端拉起）
	if len(os.Args) >= 2 && os.Args[1] == "-server" {
		startServer(cfg)
		return
	}
	if cfg != nil && cfg.debug {
		rawLog(fmt.Sprintf("脚本启动 | 参数数=%d | argv=%v", len(os.Args), os.Args))
	}

	// 模式判定：check_ai.exe -polish <input.txt> <output.txt>
	// v5.2 起错字检查（F8）已移除，CLI 仅保留润色模式
	if len(os.Args) < 4 || os.Args[1] != "-polish" {
		fmt.Println("用法: check_ai.exe -polish <input.txt> <output.txt>")
		fmt.Println("  润色语句: check_ai.exe -polish in.txt out.txt")
		if cfg != nil && cfg.debug {
			rawLog(fmt.Sprintf("退出: 参数不合法，argv=%v", os.Args))
		}
		return
	}
	inPath, outPath := os.Args[2], os.Args[3]

	// 读输入文本（容忍 UTF-8 BOM）
	data, err := os.ReadFile(inPath)
	if err != nil {
		if cfg != nil && cfg.debug {
			rawLog(fmt.Sprintf("读取输入文件失败: %v", err))
		}
		return
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if text == "" {
		if cfg != nil && cfg.debug {
			rawLog("退出: 输入文本为空")
		}
		return
	}

	debugLog(cfg, fmt.Sprintf("开始润色 | 模型=%s | base_url=%s | 文本长度=%d",
		cfg.model, cfg.baseURL, len(text)))

	// 未配置 Key
	if cfg == nil || cfg.apiKey == "" {
		debugLog(cfg, "结果: __NO_KEY__")
		_ = os.WriteFile(outPath, []byte("__NO_KEY__\n"), 0644)
		return
	}

	// 调用云端 AI（内部含分块与缓存，超长自动切片）
	debugLog(cfg, fmt.Sprintf("发送请求 | text长度=%d | model=%s | url=%s", len(text), cfg.model, cfg.baseURL))
	start := time.Now()
	content, err := processPolish(cfg, text)
	if err != nil {
		elapsed := time.Since(start).Seconds()
		debugLog(cfg, fmt.Sprintf("调用失败 | 耗时=%.2fs | %v", elapsed, err))
		_ = os.WriteFile(outPath, []byte(fmt.Sprintf("__ERROR__%v\n", err)), 0644)
		return
	}
	elapsed := time.Since(start).Seconds()
	preview := strings.ReplaceAll(content, "\n", " ")
	preview = strings.ReplaceAll(preview, "\r", " ")
	if len(preview) > 200 {
		preview = preview[:200]
	}
	debugLog(cfg, fmt.Sprintf("收到响应 | 耗时=%.2fs | 预览=%s", elapsed, preview))

	// 直接写润色后文本
	polished := cleanPolish(content)
	if polished == "" {
		debugLog(cfg, "解析结果 | 润色文本为空")
		_ = os.WriteFile(outPath, []byte("__ERROR__空响应\n"), 0644)
		return
	}
	debugLog(cfg, fmt.Sprintf("解析结果 | 润色后长度=%d", len(polished)))
	_ = os.WriteFile(outPath, []byte(polished), 0644)
}
