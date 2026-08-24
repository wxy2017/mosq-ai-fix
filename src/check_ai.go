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
// v4.5 变更：专攻"合法词但用错"的音近字误用（如 出了→除）
//   - 新增★音近字强制排查：对每个字逐一试换同音/近音字看是否更通顺
//   - step A 强化为逐字近音替换检查（最高频漏报点）
//   - temperature 0.1→0.2，降低过度保守
// v4.4 变更（召回优先：用户勾选确认，误报成本低）：
//   - wrong 必须是原文逐字一致的连续片段（防一键修正失配、提升召回）
//   - 错误类型扩到 8 类：新增"易混同音词""多字叠字""漏字缺字"
//   - 客观错别字类"宁可多报"，主观用词不当仍需语义明显
//   - 逐句覆盖、抑制空数组偏置、加 few-shot 示例
const systemPrompt = `你是一个严谨的中文校对助手，负责检查用户输入文字中的错别字、用词不当及各类文字错误。
采用【严格召回模式】：客观错别字类宁可多报可商榷的疑似错误，也不要漏报（最终由用户逐条确认，误报成本低）。

请逐句默读检查全文，不要只看开头几句，对每一句都要认真排查，以下错误类型都要找：
1. 错别字：同音字、形近字误用，要逐字比对尤其警惕形近字（如"因该"应为"应该"、"我门"应为"我们"、"未"应为"末"、"己"应为"已"）
2. 易混同音/近音词：登录/登陆（应为登录）、截至/截止、必须/必需、启示/启事、作为/做为（应为作为）、权利/权力。只在语境明显用错方向时才报，方向必须报对，不要把本就正确的写法改成另一种
3. 成语误用：如"按步就班"应为"按部就班"、"一愁莫展"应为"一筹莫展"
4. 的/地/得 误用
5. 多字/叠字重复：如"的的"、"了了"、"是是"
6. 漏字/缺字：因漏字导致语句不通顺（把含漏字的片段整段替换补全）
7. 网络谐音错别字：灰常/非常、杯具/悲剧、神马/什么、木有/没有、酱紫/这样子
8. 明显的用词不当/语句不通顺：词义混淆、介词或助词误用、明显搭配错误（如"在次感谢"应为"再次感谢"）。仅报这类语义明显错误的项
★ 音近字误用（最易漏报，务必对每个字逐一排查）：句中某词单独看是合法词，但放进本句意思不通顺；若把其中某字换成同音/近音字后整句才通顺、才符合本意——即使该词本身合法也必须报。典型：'出了以上方法'应为'除了以上方法'（出→除 近音）、'既使下雨'应为'即使下雨'（既→即）、'以经完成'应为'已经完成'（以→已）、'按装'应为'安装'（按→安）、'防碍'应为'妨碍'（防→妨）。判断口诀：该词合法≠用对，要看整句意思。

输出要求（严格遵守）：
- wrong 必须是【原文中逐字一致的连续片段】，right 是把它替换成的正确写法（字数可多可少）
- 同一错误多次出现只报一次
- 以下不算错误，不要报：人名、地名、专业术语、英文、数字、已通用的网络词（吐槽、网红、给力、打卡、yyds、绝绝子）
- 不要改写句子、不要改标点、不要改句式、不要重组语序
- 只要发现 1 个及以上疑似错误就务必报出，不要为了"保险"而输出空数组；确实没有才输出空数组

逐字排查要求（内部默想，不要输出分析过程）：对句中每个字都过一遍——有没有同音或近音的字，替换后整句更通顺、更符合本意？尤其★音近字类，该词合法≠用对，不要因为某词是合法词就跳过。

只输出 JSON，不要输出任何其他文字，格式：
{"errors":[{"wrong":"错误写法","right":"正确写法","reason":"简短原因，如: 错别字/谐音字/易混词/成语/的得地/叠字/漏字/用词不当"}]}

示例（仅示意格式）：
输入"我觉得因该出了以上方法，在次感谢"
{"errors":[{"wrong":"因该","right":"应该","reason":"错别字"},{"wrong":"出了","right":"除了","reason":"用词不当"},{"wrong":"在次","right":"再次","reason":"易混词"}]}`

// polishPrompt 润色模式（v5.0）：改写得更得体、通顺、易理解，不改原意
const polishPrompt = `你是一个中文文字润色助手，把用户输入的语句改写得更得体、通顺、易理解。
要求：
1. 保持原意不变：不增删事实性内容，不改动数字、英文、人名、地名、产品名、专业术语
2. 修正语序不通顺、口语化冗余、重复啰嗦、搭配不当、语气生硬等问题，
   使表达更书面、得体、流畅
3. 不要过度改写：保持原文的语气和自然度，避免文艺腔、过度书面化；
   原文已通顺得体时，原样返回即可
4. 保留原文的段落结构，不改变整体意思和语气风格（如正式/轻松）的基调

只输出润色后的文本本身，不要任何解释、不要加引号、不要用代码块包裹。`

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

	// 超长截断（按字符 rune 截断，避免按字节切出半个汉字产生乱码）
	runes := []rune(text)
	if len(runes) > cfg.maxText {
		text = string(runes[:cfg.maxText])
	}

	// 调用云端 AI
	debugLog(cfg, fmt.Sprintf("发送请求 | text长度=%d | model=%s | url=%s", len(text), cfg.model, cfg.baseURL))
	start := time.Now()
	prompt := systemPrompt
	if polishMode {
		prompt = polishPrompt
	}
	content, err := callAPI(cfg, prompt, text)
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
