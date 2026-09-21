// check_ai.go — 文本润色工具（Go 版）· 云端大模型
//
// 用法: check_ai.exe -polish    <input.txt> <output.txt>   润色
//       check_ai.exe -translate <input.txt> <output.txt>   翻译成简体中文
//   output.txt 直接写处理后的整段文本
//   特殊标记: __NO_KEY__ 未配置Key | __ERROR__xxx 失败
//
// 常驻服务模式: check_ai.exe -server
//   监听 127.0.0.1:<[server] port>，提供
//     POST /polish     润色（前端 F9 调用）
//     POST /translate  翻译成简体中文（前端 F10 调用）
//     POST /shutdown   优雅退出（供前端在退出时回收本进程）
//
// v5.2 起移除错字检查（F8）功能，本程序专注语句润色与翻译。
// v5.3 起移除磁盘缓存（原 <部署根>/.cache），每次调用均直连云端 API。
// v5.5 起配置支持热重载：每次请求前按 app.ini 的内容指纹判断是否重读，
//   故改动 base_url/model/api_key 后无需重启本进程即刻生效（旧版本只在进程
//   启动时读一次配置，而"关闭前端"并不会结束本进程，于是长期使用旧配置）。
// v5.6 起新增翻译成中文能力：新增 /translate 端点与 -translate CLI 模式，
//   与润色共用分块/日志/错误映射链路，仅提示词不同（translatePrompt）。
// 部署布局（v5.3）：本 exe 位于 <部署根>\runtime\，其上一级即部署根
//   - 读取 <部署根>/config/app.ini（[ai]/[log]/[server] 段）
//   - 调用云端 API（润色提示词）
//   - [log] enabled=true 时写 <部署根>/config/ai_debug.log
//
// 编译: 推荐 build\build.ps1；或 cd src && go build -o ../dist/runtime/check_ai.exe ./backend
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
	"sync"
	"time"
	"unicode/utf8"
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

// translatePrompt 翻译模式：把用户选中的文本翻译成简体中文（v5.6 新增，F10）
// 与润色刻意区分：翻译要"忠实"，不得改写语气与信息量；已是中文时原样返回，
// 避免"中文翻中文"把原文改坏。
const translatePrompt = `你是一个专业的中文翻译，负责把用户提供的文本翻译成简体中文。
任务：把用户提供的文本翻译成自然、准确、通顺的简体中文。
核心原则：
1. 忠实原文：不增删内容、不解释、不补充背景，不改变原意、语气与立场。
2. 通顺自然：符合中文表达习惯，避免逐字直译造成的欧化句式与生硬语序。
3. 术语与专名：人名、地名、机构名、产品名、专业术语采用通行译法；无通行译法时保留原文，可在其后用括号补中文（如 Kubernetes（容器编排系统））。
4. 保留格式：保持原有换行与段落划分；列表、编号、代码、公式、URL、邮箱、变量名等保持原样。
5. 已是中文：若原文已经是中文，不要改写、不要润色，原样返回；若为繁体中文则转为简体。
6. 无有效内容：若文本只是符号、数字或无法翻译的内容，原样返回。
输出要求：
- 只输出译文本身，不要添加任何说明、注释、原文对照或语言标注。
- 不要使用代码块、引号或任何额外符号包裹输出。
- 保持输出与输入的换行格式一致。
示例：
输入：The meeting has been postponed to next Monday.
输出：会议已推迟到下周一。
输入：Merci beaucoup, à bientôt !
输出：非常感谢，回头见！`

// ---------- 日志运行期参数（main 中由配置注入）----------

const (
	logMaxBytesDefault = 5 * 1024 * 1024 // 日志体积上限默认 5MB
	logKeepRatio       = 4               // 超限时保留 4/5 …
	logKeepDenom       = 5               // …即丢弃最旧的 1/5
	logFieldMaxBytes   = 1 << 20         // 请求参数/响应体的单字段预截断阈值（1MB）
	logFileName        = "ai_debug.log"  // 日志文件名（[log] dir 只指定目录时用这个名字）
)

var (
	logEnabled  = true
	logMaxBytes = int64(logMaxBytesDefault)
	logMu       sync.Mutex   // 串行化写入与裁剪：常驻服务会并发处理请求
	logSink     func(string) // 仅测试使用：非 nil 时日志交给该回调，不落盘

	// baseDir 部署根（= check_ai.exe 的上一级目录，main 中初始化）。
	// [log] dir 给的相对路径一律相对它解析 —— 不依赖进程工作目录，
	// 这样"双击启动"和"命令行启动"得到的路径一致。
	baseDir = ""
)

// ---------- 配置 ----------

type config struct {
	enabled    bool
	apiKey     string
	model      string
	baseURL    string
	timeout    int
	maxText    int
	logEnabled bool
	logMaxMB   int
	logDir     string // [log] dir：日志输出位置；空 = 用默认目录（部署根\config）
	serverPort string
}

// parseConfig 解析 app.ini 的文本内容（兼容 ; 和 # 注释）
func parseConfig(data []byte) *config {
	cfg := &config{
		model:      "glm-4-flash",
		baseURL:    "https://open.bigmodel.cn/api/paas/v4/chat/completions",
		timeout:    8,
		maxText:    500,
		logEnabled: true, // 调用日志默认开启
		logMaxMB:   5,
		logDir:     "", // 空 = 默认目录（部署根\config）
		serverPort: "18765",
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
			case "enabled", "debug": // debug 为 v5.2 及更早的旧键名，保留兼容
				cfg.logEnabled = strings.EqualFold(val, "true")
			case "max_size_mb":
				if n, err := strconv.Atoi(val); err == nil && n > 0 {
					cfg.logMaxMB = n
				}
			case "dir", "path": // 日志输出位置；两个键名等价，同一文件里以靠后的为准
				cfg.logDir = val
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

// loadConfig 读取并解析 <工具根>/config/app.ini；文件不可读时返回 nil
func loadConfig(path string) *config {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseConfig(data)
}

// ---------- 配置热重载（v5.5）----------
// 背景（历史 BUG）：常驻服务 check_ai.exe -server 是"分离启动"的进程，用户从托盘
// 退出工具时它并不会结束。而配置原先只在进程启动时读一次，于是：
//
//	改完 app.ini → 重启前端 → EnsureServer() 看到端口有人应答就直接复用旧进程
//	→ 旧进程内部仍是**旧 base_url / model / api_key**，日志里始终是旧地址。
//
// 修复思路：不再"读一次记一辈子"，而是按 **文件内容指纹** 惰性热重载 ——每次 /polish
// 请求前比一次内容，变了就重建配置并同步日志参数，使配置改动即时生效，
// 即使当前仍是那个"遗留进程"也无害。内容比对（而非 mtime/size）可避开
// "同长度改写""编辑器改时间戳"等边界情况，1.5KB 的文件每次读一遍开销可忽略。
type configStore struct {
	path string

	mu  sync.RWMutex
	cfg *config
	raw []byte // 上一次解析时的文件原始内容，用作指纹
}

func newConfigStore(path string) *configStore {
	s := &configStore{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		data = nil // 配置缺失：用内置兜底值，仍保留日志能力
	}
	s.reloadFrom(data, false)
	return s
}

// current 返回当前配置；若 app.ini 内容已变化则先重载。
// 读取失败（如文件被编辑器短暂锁定）时沿用上一次的配置，绝不把配置清空。
func (s *configStore) current() *config {
	data, err := os.ReadFile(s.path)
	if err != nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.cfg
	}
	s.mu.RLock()
	same := bytes.Equal(data, s.raw)
	prev := s.cfg
	s.mu.RUnlock()
	if same {
		return prev
	}
	return s.reloadFrom(data, true)
}

// reloadFrom 用给定内容重建配置；changed 仅用于决定是否记录一条重载日志。
func (s *configStore) reloadFrom(data []byte, changed bool) *config {
	cfg := parseConfig(data)
	s.mu.Lock()
	old := s.cfg
	s.cfg, s.raw = cfg, data
	s.mu.Unlock()

	applyLogConfig(cfg) // 日志开关/上限/落盘位置都要跟着配置刷新（含 [log] dir 变更）

	if changed && old != nil {
		if old.baseURL != cfg.baseURL || old.model != cfg.model {
			rawLog(fmt.Sprintf("检测到 app.ini 变更，已热重载配置 | model=%s | base_url=%s", cfg.model, cfg.baseURL))
		} else {
			rawLog("检测到 app.ini 变更，已热重载配置（接口地址与模型未变）")
		}
	}
	return cfg
}

// ---------- 调用日志（受 [log] enabled 控制，带体积上限）----------

// ---------- 日志落盘位置解析（v5.6.1：支持 [log] dir 自定义 + 异常回退）----------
//
// 读取优先级（高 → 低）：
//   ① [log] dir（或等价别名 [log] path）配置的路径，且该位置可用
//   ② 默认目录：<部署根>\config\ai_debug.log
//   ③ 上面两个都不可用 → 关闭日志（logEnabled=false），业务功能不受影响
//
// 值的形态：
//   · 留空           → 用默认目录
//   · 以 .log 结尾   → 视为完整文件路径，直接用
//   · 其他           → 视为目录，文件名固定 ai_debug.log
//   · 相对路径       → 相对**部署根**解析（不依赖进程工作目录，避免"双击 vs 命令行"不一致）
//
// 异常处理：目录不存在 → MkdirAll 自动创建；MkdirAll 成功但文件打不开（只读盘、
// 无权限、被独占）→ 判定该位置不可用，回退到默认目录，并在默认日志里记一条警告。

// logTarget 日志落盘位置的解析结果
type logTarget struct {
	path       string // 最终日志文件绝对路径；空串 = 无处可写
	fallback   bool   // 是否从自定义位置回退到了默认目录
	failReason string // 回退/失败原因
}

// prepareLogFile 把配置值规整成日志文件绝对路径，并确保其父目录存在且文件可写。
// 空值 = 默认目录。返回 error 表示"这个位置不可用"。
//
// 防御：baseDir（部署根）未初始化时直接报错，绝不按进程工作目录去建目录 ——
// 否则测试或异常启动会在源码/当前目录里冒出 config\ai_debug.log。
func prepareLogFile(v string) (string, error) {
	v = strings.TrimSpace(v)
	var file string
	switch {
	case v == "":
		if baseDir == "" {
			return "", fmt.Errorf("部署根未初始化，无法确定默认日志目录")
		}
		file = filepath.Join(baseDir, "config", logFileName)
	case filepath.IsAbs(v):
		if strings.EqualFold(filepath.Ext(v), ".log") {
			file = v
		} else {
			file = filepath.Join(v, logFileName)
		}
	default:
		if baseDir == "" {
			return "", fmt.Errorf("部署根未初始化，无法解析相对日志路径 %s", v)
		}
		full := filepath.Join(baseDir, v)
		if strings.EqualFold(filepath.Ext(full), ".log") {
			file = full
		} else {
			file = filepath.Join(full, logFileName)
		}
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("目录 %s 无法创建（%v）", dir, err)
	}
	// MkdirAll 成功不等于可写：只读盘、ACL 拒绝、文件被独占都要靠真正试开一次才能发现
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("文件 %s 不可写（%v）", file, err)
	}
	_ = f.Close()
	return file, nil
}

// resolveLogTarget 按优先级解析日志位置，必要时回退到默认目录
func resolveLogTarget(cfg *config) logTarget {
	if strings.TrimSpace(cfg.logDir) != "" {
		if p, err := prepareLogFile(cfg.logDir); err == nil {
			return logTarget{path: p}
		} else {
			def, derr := prepareLogFile("")
			if derr != nil {
				return logTarget{fallback: true,
					failReason: fmt.Sprintf("自定义位置：%v；默认目录：%v", err, derr)}
			}
			return logTarget{path: def, fallback: true, failReason: err.Error()}
		}
	}
	p, err := prepareLogFile("")
	if err != nil {
		return logTarget{failReason: err.Error()}
	}
	return logTarget{path: p}
}

// applyLogConfig 依配置决定日志文件路径与开关，并同步刷新上限。
// 供 configStore 每次重载时调用，因此改 [log] dir 后无需重启即可生效。
func applyLogConfig(cfg *config) {
	t := resolveLogTarget(cfg)

	logMu.Lock()
	logPath = t.path
	logEnabled = cfg.logEnabled && t.path != "" // 无处可写时直接关掉，避免反复试开
	if cfg.logMaxMB > 0 {
		logMaxBytes = int64(cfg.logMaxMB) * 1024 * 1024
	} else {
		logMaxBytes = int64(logMaxBytesDefault)
	}
	logMu.Unlock()

	if t.path == "" {
		return // 连默认目录都写不了：静默关闭日志，不干扰润色/翻译
	}
	if t.fallback {
		rawLog("警告: [log] dir 指定的位置不可用，已回退到默认目录 | 原因: ", t.failReason)
		rawLog("当前实际日志文件: ", t.path)
	}
}

// currentLogPath 读取当前生效的日志文件路径（加锁，供日志/自检展示）
func currentLogPath() string {
	logMu.Lock()
	defer logMu.Unlock()
	return logPath
}

// rawLog 写一行日志："[时间] 内容"，多余参数按 fmt.Sprint 拼接。
// 受 logEnabled 开关控制；写入前若会突破 logMaxBytes，则先丢弃最旧记录。
// 单行长度被限制在上限的 1/5（logKeepDenom），因此「裁剪后保留 4/5 + 追加 ≤1/5」
// 可保证文件体积恒不超过上限，即使单条响应异常巨大也不会突破。
// 行首统一带时间戳，故即使某条调用的记录块被裁剪掉前半，剩余行仍可独立解读。
func rawLog(parts ...any) {
	logMu.Lock() // 常驻服务并发处理请求，写入与裁剪必须串行；配置热重载也会改这里的开关
	defer logMu.Unlock()

	if !logEnabled || logPath == "" {
		return
	}
	text, truncated := cutBytes(fmt.Sprint(parts...), int(logMaxBytes/logKeepDenom))
	if truncated {
		text += "...（单条日志过长已截断）"
	}
	line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] " + text + "\n"

	if logSink != nil { // 仅测试使用：捕获日志而不落盘
		logSink(line)
		return
	}

	if fi, err := os.Stat(logPath); err == nil && fi.Size()+int64(len(line)) > logMaxBytes {
		trimLogLocked()
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = io.WriteString(f, line)
}

// cutBytes 把 s 截断到至多 max 字节，且不切断 UTF-8 字符；第二个返回值为是否发生截断。
func cutBytes(s string, max int) (string, bool) {
	if max < 0 || len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// trimLogLocked 丢弃日志头部（最旧记录），只保留尾部 4/5。
// 调用方必须已持有 logMu。裁剪点对齐到行首，避免留下半行。
func trimLogLocked() {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return
	}
	keep := logMaxBytes * logKeepRatio / logKeepDenom
	if int64(len(data)) <= keep {
		return
	}
	cut := int64(len(data)) - keep
	for cut < int64(len(data)) && data[cut] != '\n' {
		cut++
	}
	if cut < int64(len(data)) {
		cut++ // 连同换行符一起丢弃
	}
	// 先写临时文件再原子替换：裁剪中途失败不会破坏原日志
	tmp := logPath + ".tmp"
	if err := os.WriteFile(tmp, data[cut:], 0644); err != nil {
		return
	}
	if err := os.Rename(tmp, logPath); err != nil {
		_ = os.Remove(tmp)
	}
}

// logField 把可能很大的请求参数/响应体转成可安全落盘的字符串，
// 超过 logFieldMaxBytes 时按 UTF-8 字符边界截断并标注原始长度。
func logField(b []byte) string {
	s, truncated := cutBytes(string(b), logFieldMaxBytes)
	if truncated {
		return s + fmt.Sprintf("...（已截断，原始 %d 字节）", len(b))
	}
	return s
}

// ---------- 智谱 API ----------

// procResult 一次文本处理的结果。
// Model 来自响应体的 model 字段 —— 它是**实际**服务本次请求的模型
// （上游/中转可能把请求里的模型名改写掉），前端标题栏展示的就是它。
type procResult struct {
	Text  string
	Model string
}

// kind 仅用于日志区分本次调用属于哪个功能（润色 / 翻译）
func callAPI(cfg *config, kind, prompt, text string) (procResult, error) {
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
		return procResult{}, err
	}
	req, err := http.NewRequest("POST", cfg.baseURL, bytes.NewReader(body))
	if err != nil {
		return procResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	// 注意：api_key 放在 Authorization 头中，不写入日志，避免密钥落盘
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)

	// ---- 调用日志：请求侧 ----
	rawLog("---- 大模型调用开始 ----")
	rawLog("调用类型: ", kind)
	rawLog("请求URL: ", cfg.baseURL)
	rawLog("请求参数: ", logField(body))

	// ⚠️ 计时点必须在**读完响应体之后**：
	// 非流式接口下大模型是"先回响应头、再花几秒把正文生成完"（实测响应头 0.26s
	// 就回来了，正文 7s 才到），若把计时停在 client.Do 返回处，日志会报出
	// "只花了 0.1 秒"的错误结论——真实耗时其实是这里的 total。
	start := time.Now()
	resp, err := callHTTP(cfg, req)
	if err != nil {
		rawLog(fmt.Sprintf("调用结果: 失败 | 耗时=%.2fs | %v", time.Since(start).Seconds(), err))
		rawLog("---- 大模型调用结束 ----")
		return procResult{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	total := time.Since(start)
	if err != nil {
		rawLog(fmt.Sprintf("调用结果: 读取响应失败 | 耗时=%.2fs | %v", total.Seconds(), err))
		rawLog("---- 大模型调用结束 ----")
		return procResult{}, err
	}

	// ---- 调用日志：响应侧（含完整响应体）----
	rawLog(fmt.Sprintf("响应状态: HTTP %d | 总耗时=%.2fs | 响应体 %d 字节",
		resp.StatusCode, total.Seconds(), len(respBody)))
	rawLog("完整响应: ", logField(respBody))
	rawLog("---- 大模型调用结束 ----")

	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return procResult{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	var data struct {
		// model 是**实际**服务本次请求的模型名，可能被上游/中转改写，
		// 与请求里填的 cfg.model 不一定相同 —— 前端标题栏展示的就是它
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return procResult{}, err
	}
	if len(data.Choices) == 0 {
		return procResult{}, fmt.Errorf("no choices in response")
	}
	return procResult{Text: data.Choices[0].Message.Content, Model: data.Model}, nil
}

// ---------- 性能优化：分块 / 常驻服务 ----------
// 注：v5.3 起已移除磁盘缓存（原 .cache 目录）。每次调用均直连云端 API，
// 取舍见记忆档（放弃"同句二次调用免请求"，换取确定性失效逻辑与零磁盘残留）。

var callHTTP = doHTTP

// clientMu 保护 httpClient/httpClientTimeout：热重载可能改超时时间，届时重建客户端
var (
	clientMu          sync.Mutex
	httpClient        *http.Client
	httpClientTimeout int
)

// clientFor 返回超时为 timeout 秒的 HTTP 客户端，并复用连接池。
// 超时时间变化时重建（旧客户端的 Timeout 是写死的），这使 [ai] timeout 的热重载也生效。
func clientFor(timeout int) *http.Client {
	if timeout <= 0 {
		timeout = 8
	}
	clientMu.Lock()
	defer clientMu.Unlock()
	if httpClient == nil || httpClientTimeout != timeout {
		httpClient = &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 90 * time.Second,
			},
		}
		httpClientTimeout = timeout
	}
	return httpClient
}

// doHTTP 用配置中的超时发起请求（实际发送逻辑）
func doHTTP(cfg *config, req *http.Request) (*http.Response, error) {
	return clientFor(cfg.timeout).Do(req)
}

// corePolish 单段润色
// coreText 单次调用（kind 只用于日志区分功能）
func coreText(cfg *config, kind, prompt, text string) (procResult, error) {
	return callAPI(cfg, kind, prompt, text)
}

// segmented 通用入口：文本不超过 cfg.maxText 时一次调用；超长则切片并拼接。
// 润色与翻译共用（两者都只是"换个提示词调用大模型"，分块策略完全一致）。
// Model 取最后一个非空值：同一批分块通常是同一个模型，取到即可供界面展示。
func segmented(cfg *config, kind, prompt, text string) (procResult, error) {
	runes := []rune(text)
	if len(runes) <= cfg.maxText {
		return coreText(cfg, kind, prompt, text)
	}
	overlap := 30
	var sb strings.Builder
	model := ""
	for i := 0; i < len(runes); i += (cfg.maxText - overlap) {
		end := i + cfg.maxText
		if end > len(runes) {
			end = len(runes)
		}
		res, err := coreText(cfg, kind, prompt, string(runes[i:end]))
		if err != nil {
			return procResult{}, err
		}
		sb.WriteString(cleanOutput(res.Text))
		if res.Model != "" {
			model = res.Model
		}
		if end == len(runes) {
			break
		}
	}
	return procResult{Text: sb.String(), Model: model}, nil
}

// processPolish 润色入口
func processPolish(cfg *config, text string) (procResult, error) {
	return segmented(cfg, "润色", polishPrompt, text)
}

// processTranslate 翻译入口：把文本翻译成简体中文（v5.6 新增，前端 F10 调用）
func processTranslate(cfg *config, text string) (procResult, error) {
	return segmented(cfg, "翻译", translatePrompt, text)
}

// modelHeader 把"实际使用的模型名"回给前端的响应头。
// 走响应头而不改响应体：现有前端契约是纯文本（含 __NO_KEY__ / __ERROR__ 前缀），
// 改成 JSON 会破坏它，也会动到全部既有测试。
const modelHeader = "X-Model"

// textHandler 生成文本处理端点（/polish 与 /translate 共用同一套
// 取配置→读 body→错误映射逻辑，只是处理函数不同）
func textHandler(store *configStore, run func(*config, string) (procResult, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ★ 每个请求都取一次当前配置（内部按文件内容指纹判是否重载）。
		// 这样即便本进程是上一次遗留的"僵尸后端"，改了 app.ini 后下一次调用
		// 也会立刻用上新 base_url / model / api_key，而不是进程启动时的旧值。
		cfg := store.current()
		if cfg == nil || cfg.apiKey == "" {
			io.WriteString(w, "__NO_KEY__")
			return
		}
		body, _ := io.ReadAll(r.Body)
		text := strings.TrimSpace(string(body))
		if text == "" {
			// 空文本直接返回空串（不调用大模型）：供前端做存活/连通性探针
			io.WriteString(w, "")
			return
		}
		res, err := run(cfg, text)
		if err != nil {
			io.WriteString(w, "__ERROR__"+err.Error())
			return
		}
		if strings.TrimSpace(res.Text) == "" {
			io.WriteString(w, "__ERROR__空响应")
			return
		}
		// 头必须在写 body 之前设置；拿不到模型名就不发该头，前端会隐藏对应展示
		if res.Model != "" {
			w.Header().Set(modelHeader, res.Model)
		}
		io.WriteString(w, res.Text)
	}
}

// startServer 常驻本地服务：复用连接池，提供 /polish、/translate、/shutdown
func startServer(store *configStore) {
	cfg := store.current() // 启动时读一次，仅用于绑定端口与建连接池
	clientFor(cfg.timeout)

	mux := http.NewServeMux()
	// 业务端点：两者共用 textHandler（取配置、读 body、错误映射一致）
	mux.HandleFunc("/polish", textHandler(store, processPolish))
	mux.HandleFunc("/translate", textHandler(store, processTranslate))

	// /shutdown：前端退出时调用，请本进程优雅退出。
	// 存在的意义：后端是"分离启动"的常驻进程，不随前端退出而结束；
	// 若不主动收掉，它会一直占用端口并锁定 check_ai.exe 文件（阻碍重新构建/升级）。
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		rawLog("收到 /shutdown，常驻服务即将退出")
		io.WriteString(w, "bye")
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // 先把响应发出去，再退出，避免前端拿到连接中断
		}
		go func() {
			time.Sleep(120 * time.Millisecond)
			os.Exit(0)
		}()
	})

	addr := "127.0.0.1:" + cfg.serverPort
	// 把实际生效的日志路径写进日志，便于确认 [log] dir 是否按预期落位
	rawLog(fmt.Sprintf("常驻服务启动 | 监听 %s | model=%s | base_url=%s | 日志=%s",
		addr, cfg.model, cfg.baseURL, currentLogPath()))
	_ = http.ListenAndServe(addr, mux)
}

// ---------- 结果处理 ----------

// cleanOutput 清理模型输出：去掉代码块包裹和误加的首尾引号（润色与翻译共用）
func cleanOutput(s string) string {
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
	exeDir := filepath.Dir(exePath) // .../dist/runtime
	rootDir := filepath.Dir(exeDir) // 部署根（dist/）
	iniPath := filepath.Join(rootDir, "config", "app.ini")

	// 部署根是全局基准：启动器与 [log] dir 的相对路径都相对它解析
	baseDir = rootDir

	// 日志文件路径由配置决定（[log] dir），未配置则用默认目录 <部署根>\config；
	// 具体解析与"不可写则回退"的逻辑都在 applyLogConfig 里，随 configStore 一起生效
	store := newConfigStore(iniPath)
	cfg := store.current()

	// 常驻服务模式：启动本地 HTTP 服务（由前端拉起，之后常驻）
	if len(os.Args) >= 2 && os.Args[1] == "-server" {
		startServer(store)
		return
	}
	rawLog(fmt.Sprintf("脚本启动 | 参数数=%d | argv=%v", len(os.Args), os.Args))

	// 模式判定：check_ai.exe -polish    <input.txt> <output.txt>
	//           check_ai.exe -translate <input.txt> <output.txt>
	// v5.2 起错字检查（F8）已移除，CLI 只保留"润色"与"翻译"两种一次性调用
	mode := ""
	if len(os.Args) >= 2 {
		mode = os.Args[1]
	}
	if len(os.Args) < 4 || (mode != "-polish" && mode != "-translate") {
		fmt.Println("用法: check_ai.exe -polish    <input.txt> <output.txt>   # 润色")
		fmt.Println("      check_ai.exe -translate <input.txt> <output.txt>   # 翻译成中文")
		fmt.Println("      check_ai.exe -server                              # 常驻本地服务")
		rawLog(fmt.Sprintf("退出: 参数不合法，argv=%v", os.Args))
		return
	}
	run := processPolish
	verb := "润色"
	if mode == "-translate" {
		run = processTranslate
		verb = "翻译"
	}
	inPath, outPath := os.Args[2], os.Args[3]

	// 读输入文本（容忍 UTF-8 BOM）
	data, err := os.ReadFile(inPath)
	if err != nil {
		rawLog(fmt.Sprintf("读取输入文件失败: %v", err))
		return
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if text == "" {
		rawLog("退出: 输入文本为空")
		return
	}

	rawLog(fmt.Sprintf("开始%s | 模型=%s | base_url=%s | 文本长度=%d",
		verb, cfg.model, cfg.baseURL, len(text)))

	// 未配置 Key
	if cfg == nil || cfg.apiKey == "" {
		rawLog("结果: __NO_KEY__")
		_ = os.WriteFile(outPath, []byte("__NO_KEY__\n"), 0644)
		return
	}

	// 调用云端 AI（超长自动切片）
	rawLog(fmt.Sprintf("发送请求 | text长度=%d | model=%s | url=%s", len(text), cfg.model, cfg.baseURL))
	start := time.Now()
	res, err := run(cfg, text)
	if err != nil {
		elapsed := time.Since(start).Seconds()
		rawLog(fmt.Sprintf("调用失败 | 耗时=%.2fs | %v", elapsed, err))
		_ = os.WriteFile(outPath, []byte(fmt.Sprintf("__ERROR__%v\n", err)), 0644)
		return
	}
	elapsed := time.Since(start).Seconds()

	// 写结果文本（完整响应体已由 callAPI 记入日志，此处不再重复记录预览）
	result := cleanOutput(res.Text)
	rawLog(fmt.Sprintf("收到响应 | 耗时=%.2fs | 实际模型=%s | %s后长度=%d",
		elapsed, res.Model, verb, len(result)))
	if result == "" {
		rawLog(fmt.Sprintf("解析结果 | %s文本为空", verb))
		_ = os.WriteFile(outPath, []byte("__ERROR__空响应\n"), 0644)
		return
	}
	_ = os.WriteFile(outPath, []byte(result), 0644)
}
