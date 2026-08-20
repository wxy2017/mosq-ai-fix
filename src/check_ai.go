// check_ai.go — 文本错字检查（Go 版）· 智谱 GLM-4-Flash 云端校对
//
// 用法: check_ai.exe <input.txt> <output.txt>
//   input.txt  : UTF-8 待检查文本
//   output.txt : 结果输出，每行一条: 错误词(Tab)正确词(Tab)原因
//                特殊标记: __NO_KEY__ 未配置Key | __NONE__ 无错误 | __ERROR__xxx 失败
//
// 与 Python 版 check_ai.py 接口完全一致，行为对齐：
//   - 读取 <工具根>/config/typo_config.ini（[ai]/[log] 两个段）
//   - 调用智谱 API（严格模式提示词）
//   - debug=true 时写 <工具根>/config/ai_debug.log
//
// 编译: go build -o bin/check_ai.exe .
// 本程序仅使用 Go 标准库，无第三方依赖，免安装、免 Python 环境。

package main

import (
	"bytes"
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

// systemPrompt 与 Python 版完全一致（严格模式：含网络谐音错别字）
const systemPrompt = `你是一个严谨的中文错别字校对助手，只负责检查用户输入文字中的错别字。
采用【严格模式】：尽量多地找出错误，包括网络谐音错别字。

检查这些错误类型：
1. 错别字（同音字、形近字误用，如"因该"应为"应该"）
2. 成语误用（如"按步就班"应为"按部就班"）
3. "的/地/得"误用
4. 常见音近、形近错误
5. 网络谐音错别字（如"灰常"应为"非常"、"杯具"应为"悲剧"、"神马"应为"什么"、"木有"应为"没有"、"酱紫"应为"这样子"）

严格要求：
- 对你【有较高把握】的错误都要报出来，包括上述网络谐音错别字，不要漏报
- 以下【不算】错别字，不要报：人名、地名、专业术语、英文、数字，
  以及已成通用书面词的网络流行语（如 吐槽、网红、给力、打卡、yyds、绝绝子）
- 不要改写句子、不要改标点、不要修改句式
- 同一错误多次出现只报一次
- 没有错误时输出空数组

只输出 JSON，不要输出任何其他文字，格式：
{"errors": [{"wrong": "错误写法", "right": "正确写法", "reason": "简短原因(5字内)"}]}`

var logPath string // ai_debug.log 绝对路径（main 中初始化）

// ---------- 配置 ----------

type config struct {
	enabled bool
	apiKey  string
	model   string
	baseURL string
	timeout int
	maxText int
	debug   bool
}

// loadConfig 解析 <工具根>/config/typo_config.ini（兼容 ; 和 # 注释）
func loadConfig(path string) *config {
	cfg := &config{
		model:   "glm-4-flash",
		baseURL: "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		timeout: 8,
		maxText: 500,
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

func callAPI(cfg *config, text string) (string, error) {
	payload := map[string]any{
		"model": cfg.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": text},
		},
		"temperature": 0.1,
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

	client := &http.Client{Timeout: time.Duration(cfg.timeout) * time.Second}
	resp, err := client.Do(req)
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
	if cfg != nil && cfg.debug {
		rawLog(fmt.Sprintf("脚本启动 | 参数数=%d | argv=%v", len(os.Args), os.Args))
	}

	if len(os.Args) < 3 {
		fmt.Println("用法: check_ai.exe <input.txt> <output.txt>")
		fmt.Println("  例: check_ai.exe in.txt out.txt")
		if cfg != nil && cfg.debug {
			rawLog("退出: 参数不足，需要 input.txt 和 output.txt 两个参数")
		}
		return
	}
	inPath, outPath := os.Args[1], os.Args[2]

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

	debugLog(cfg, fmt.Sprintf("开始检查 | 模型=%s | base_url=%s | 文本长度=%d",
		cfg.model, cfg.baseURL, len(text)))

	// 未配置 Key
	if cfg == nil || cfg.apiKey == "" {
		debugLog(cfg, "结果: __NO_KEY__")
		_ = os.WriteFile(outPath, []byte("__NO_KEY__\n"), 0644)
		return
	}

	// 超长截断（按字符 rune 截断，避免按字节切出半个汉字产生乱码）
	runes := []rune(text)
	if len(runes) > cfg.maxText {
		text = string(runes[:cfg.maxText])
	}

	// 调用云端 AI
	debugLog(cfg, fmt.Sprintf("发送请求 | text长度=%d | model=%s | url=%s", len(text), cfg.model, cfg.baseURL))
	start := time.Now()
	content, err := callAPI(cfg, text)
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

	errors := parseErrors(content)
	debugLog(cfg, fmt.Sprintf("解析结果 | 错误数=%d", len(errors)))
	if len(errors) == 0 {
		_ = os.WriteFile(outPath, []byte("__NONE__\n"), 0644)
		return
	}
	var sb strings.Builder
	for _, e := range errors {
		sb.WriteString(e.Wrong + "\t" + e.Right + "\t" + e.Reason + "\n")
		debugLog(cfg, fmt.Sprintf("识别错误 | %s -> %s | %s", e.Wrong, e.Right, e.Reason))
	}
	_ = os.WriteFile(outPath, []byte(sb.String()), 0644)
}
