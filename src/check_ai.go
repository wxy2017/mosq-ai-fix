// check_ai.go — 文本校对工具（Go 版）· 智谱 GLM-4-Flash 云端
//
// 用法: check_ai.exe [-polish] <input.txt> <output.txt>
//   无 -polish（默认，错字/用词检查）:
//     output.txt 每行一条: 错误词(Tab)正确词(Tab)原因
//     特殊标记: __NO_KEY__ 未配置Key | __NONE__ 无错误 | __ERROR__xxx 失败
//   -polish（v5.0 新增，语句润色）:
//     output.txt 直接写润色后的整段文本
//     特殊标记: __NO_KEY__ 未配置Key | __ERROR__xxx 失败
//
// 与 Python 版 check_ai.py 接口完全一致，行为对齐：
//   - 读取 <工具根>/config/typo_config.ini（[ai]/[log] 两个段）
//   - 调用智谱 API（检查用严格模式提示词，润色用润色提示词）
//   - debug=true 时写 <工具根>/config/ai_debug.log
//
// 编译: go build -o bin/check_ai.exe .
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

// systemPrompt 严格召回模式：错别字 + 用词不当 + 易混词/叠字/漏字/音近字（v4.5）
const systemPrompt = `你是一个严谨的中文校对助手，采用严格召回模式：宁可多报可商榷的疑似错误，也不要漏报（最终由用户逐条确认，误报成本低）。

请检查以下错误类型：
1. 错别字（同音字、形近字误用，如"因该"→"应该"、"我门"→"我们"）
2. 易混同音/近音词：登录/登陆（应为登录）、截至/截止、必须/必需、启示/启事、作为/做为（应为作为）、权利/权力
3. 成语误用：如"按步就班"→"按部就班"、"一愁莫展"→"一筹莫展"
4. 的/地/得误用
5. 多字/叠字重复：如"的的"、"了了"、"是是"
6. 漏字/缺字：因漏字导致语句不通顺（把含漏字的片段整段替换补全）
7. 网络谐音错别字：灰常→非常、杯具→悲剧、神马→什么、木有→没有、酱紫→这样子
8. 明显的用词不当/语句不通顺：词义混淆、介词或助词误用、明显搭配错误（如"在次感谢"→"再次感谢"）
★ 音近字误用（最易漏报，务必对每个字逐一排查）：句中某词单独看是合法词，但放进本句意思不通顺；若把其中某字换成同音/近音字后整句才通顺、才符合本意——即使该词本身合法也必须报。典型：'出了以上方法'→'除了以上方法'（出→除 近音）、'既使下雨'→'即使下雨'（既→即）、'以经完成'→'已经完成'（以→已）、'按装'→'安装'（按→安）、'防碍'→'妨碍'（防→妨）

输出要求：
- wrong 必须是原文中逐字一致的连续片段，right 是替换后的正确写法（字数可多可少）
- 同一错误多次出现只报一次
- 不要报：人名、地名、专业术语、英文、数字、已通用的网络词（吐槽、网红、给力、打卡、yyds、绝绝子）
- 不要改写句子、不要改标点、不要改句式、不要重组语序
- 只要发现 1 个及以上疑似错误就务必报出，不要为了“保险”而输出空数组；确实没有才输出空数组
- 只输出 JSON，不要输出任何其他文字，格式：{"errors":[{"wrong":"错误写法","right":"正确写法","reason":"简短原因，如: 错别字/谐音字/易混词/成语/的得地/叠字/漏字/用词不当"}]}

示例：
输入："我觉得因该出了以上方法，在次感谢"
输出：{"errors":[{"wrong":"因该","right":"应该","reason":"错别字"},{"wrong":"出了","right":"除了","reason":"用词不当"},{"wrong":"在次","right":"再次","reason":"易混词"}]}`

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

const promptVersion = "v4.5" // 缓存失效标记：提示词变更时改此值

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

// coreCheck 单段检查（含缓存）；返回模型原始 JSON 或缓存内容
func coreCheck(cfg *config, text string) (string, error) {
	key := "check|" + promptVersion + "|" + cfg.model + "|" + cfg.baseURL + "|" + text
	if data, ok := cacheGet(key); ok {
		return string(data), nil
	}
	content, err := callAPI(cfg, systemPrompt, text)
	if err != nil {
		return "", err
	}
	cachePut(key, []byte(content))
	return content, nil
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

// processCheck 对外检查入口：超长自动分块（带重叠），合并去重
func processCheck(cfg *config, text string) (string, error) {
	runes := []rune(text)
	if len(runes) <= cfg.maxText {
		return coreCheck(cfg, text)
	}
	overlap := 30
	merged := make([]errItem, 0)
	seen := make(map[string]bool)
	for i := 0; i < len(runes); i += (cfg.maxText - overlap) {
		end := i + cfg.maxText
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		content, err := coreCheck(cfg, chunk)
		if err != nil {
			return "", err
		}
		for _, e := range parseErrors(content) {
			k := e.Wrong + "\t" + e.Right
			if !seen[k] {
				seen[k] = true
				merged = append(merged, e)
			}
		}
		if end == len(runes) {
			break
		}
	}
	if len(merged) == 0 {
		return "__NONE__", nil
	}
	b, _ := json.Marshal(apiResult{Errors: merged})
	return string(b), nil
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

// startServer 常驻本地服务：复用连接池，提供 /check /polish 端点
func startServer(cfg *config) {
	httpClient = &http.Client{
		Timeout: time.Duration(cfg.timeout) * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:    10,
			IdleConnTimeout: 90 * time.Second,
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		// 未配置 Key：与 CLI 模式一致返回 __NO_KEY__
		if cfg == nil || cfg.apiKey == "" {
			io.WriteString(w, "__NO_KEY__")
			return
		}
		body, _ := io.ReadAll(r.Body)
		text := strings.TrimSpace(string(body))
		if text == "" {
			io.WriteString(w, "__NONE__")
			return
		}
		content, err := processCheck(cfg, text)
		if err != nil {
			io.WriteString(w, "__ERROR__"+err.Error())
			return
		}
		io.WriteString(w, formatCheckOutput(content))
	})
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

// ---------- 结果解析 ----------

type errItem struct {
	Wrong  string `json:"wrong"`
	Right  string `json:"right"`
	Reason string `json:"reason"`
}

type apiResult struct {
	Errors []errItem `json:"errors"`
}

func cleanField(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// parseErrors 解析模型返回的 JSON，过滤无效项
func parseErrors(content string) []errItem {
	content = strings.TrimSpace(content)
	// 去掉可能的 ```json 代码块包裹
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		} else {
			content = ""
		}
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < 0 || end <= start {
		return nil
	}
	var res apiResult
	if err := json.Unmarshal([]byte(content[start:end+1]), &res); err != nil {
		return nil
	}
	out := make([]errItem, 0, len(res.Errors))
	for _, e := range res.Errors {
		e.Wrong = strings.TrimSpace(e.Wrong)
		e.Right = strings.TrimSpace(e.Right)
		e.Reason = strings.TrimSpace(e.Reason)
		if e.Wrong == "" || e.Right == "" || e.Wrong == e.Right {
			continue
		}
		e.Wrong = cleanField(e.Wrong)
		e.Right = cleanField(e.Right)
		e.Reason = cleanField(e.Reason)
		out = append(out, e)
	}
	return out
}

// formatCheckOutput 将模型 JSON 转为前端可解析的 Tab 行格式（与 CLI 模式输出完全一致）：
//   - 无错误返回 "__NONE__"
//   - 有错误返回多行 "错误\t正确\t原因\n..."
// 供 CLI 写文件与常驻服务 /check 端点共用，保证两种调用路径输出一致
func formatCheckOutput(content string) string {
	errors := parseErrors(content)
	if len(errors) == 0 {
		return "__NONE__"
	}
	var sb strings.Builder
	for _, e := range errors {
		sb.WriteString(e.Wrong + "\t" + e.Right + "\t" + e.Reason + "\n")
	}
	return sb.String()
}

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
	// 路径基准（v4.1 目录结构）：exe 位于 <根>/src/bin/，
	// 配置与日志位于 <根>/config/ —— 从 exe 向上两级即项目根
	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	}
	exeDir := filepath.Dir(exePath)               // .../src/bin
	rootDir := filepath.Dir(filepath.Dir(exeDir)) // 工具根目录
	iniPath := filepath.Join(rootDir, "config", "typo_config.ini")
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

	// 模式判定：check_ai.exe [-polish] <input.txt> <output.txt>
	polishMode := len(os.Args) >= 4 && os.Args[1] == "-polish"
	inIdx, outIdx := 1, 2
	if polishMode {
		inIdx, outIdx = 2, 3
	}
	if len(os.Args) < outIdx+1 {
		fmt.Println("用法: check_ai.exe [-polish] <input.txt> <output.txt>")
		fmt.Println("  检查错字: check_ai.exe in.txt out.txt")
		fmt.Println("  润色语句: check_ai.exe -polish in.txt out.txt")
		if cfg != nil && cfg.debug {
			rawLog("退出: 参数不足，需要 input.txt 和 output.txt 两个参数")
		}
		return
	}
	inPath, outPath := os.Args[inIdx], os.Args[outIdx]

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

	debugLog(cfg, fmt.Sprintf("开始%s | 模型=%s | base_url=%s | 文本长度=%d",
		map[bool]string{true: "润色", false: "检查"}[polishMode],
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
	var content string
	if polishMode {
		content, err = processPolish(cfg, text)
	} else {
		content, err = processCheck(cfg, text)
	}
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

	// 润色模式：直接写润色后文本
	if polishMode {
		polished := cleanPolish(content)
		if polished == "" {
			debugLog(cfg, "解析结果 | 润色文本为空")
			_ = os.WriteFile(outPath, []byte("__ERROR__空响应\n"), 0644)
			return
		}
		debugLog(cfg, fmt.Sprintf("解析结果 | 润色后长度=%d", len(polished)))
		_ = os.WriteFile(outPath, []byte(polished), 0644)
		return
	}

	errors := parseErrors(content)
	debugLog(cfg, fmt.Sprintf("解析结果 | 错误数=%d", len(errors)))
	out := formatCheckOutput(content)
	if len(errors) == 0 {
		debugLog(cfg, "解析结果 | 无错误(__NONE__)")
	} else {
		for _, e := range errors {
			debugLog(cfg, fmt.Sprintf("识别错误 | %s -> %s | %s", e.Wrong, e.Right, e.Reason))
		}
	}
	_ = os.WriteFile(outPath, []byte(out+"\n"), 0644)
}
