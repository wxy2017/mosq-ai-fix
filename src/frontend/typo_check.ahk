#Requires AutoHotkey v2.0
#SingleInstance Force

; ---------------- 自动提权 ----------------
; VSCode 等以管理员运行的软件，普通权限的工具收不到按键（UIPI 隔离）。
; 非管理员时用 *RunAs 重新以管理员身份启动（弹一次 UAC，点是即可）；
; 用户拒绝 UAC 时降级为普通权限继续运行，微信等普通应用仍可用。
; 带参数（如 -selftest）时不提权，避免自检也弹 UAC。
if !A_IsAdmin && A_Args.Length = 0 {
    try {
        Run('*RunAs "' A_ScriptFullPath '"')
        ExitApp()
    }
}

; =============================================================
; 文本润色 · 翻译工具  v5.6（Go 版 · 免 Python 环境）
; 用法：在任意可编辑的输入框（微信聊天框、网页文本框、
;       记事本、代码编辑器等）打好字后：
;       【F9 润色】
;       方式一（只润色一段）：先选中要润色的文字，再按 F9
;             → 只润色并替换选中的这一段，选区之外的文字完全不动
;       方式二（润色全部）：不选中任何文字，直接按 F9
;             → 润色整个输入框的内容并整体替换
;       两种方式都是弹窗预览（可编辑微调），点"替换原文"才回填
;       【F10 翻译成中文】
;       选中要翻译的文字，按 F10 → 把这段翻译成中文并弹窗展示
;       不选中则把整个输入框的内容当原文（窗口里会写明翻的是哪一段）
;       译文窗口只读，不影响原文；需要回填点「复制译文」再自行粘贴
;       热键可在 config\app.ini 的 [hotkey] 段修改
;         polish_key=润色热键（默认 F9）／translate_key=翻译热键（默认 F10）
; 说明：本工具不修改任何程序，仅模拟复制/粘贴，安全无风险
;
; v5.0 变更：
;  1. 新增语句润色功能：按 F9（可配置 [hotkey] polish_key）
;     改写当前语句，使其更得体、通顺、易理解
;  2. 润色默认处理整个输入框内容（全选）
;  3. 润色结果弹窗预览，支持手动微调后"替换原文"
;  注意：润色文本会发送到云端大模型，请勿输入敏感内容
; v5.1 变更（性能优化）：
;  1. 后端常驻服务：首次使用时拉起 check_ai.exe -server 常驻本地
;     (127.0.0.1:ServerPort)，后续润色均走 HTTP 调用，
;     免去每次调用都冷启动进程的开销，并复用连接池
;  2. 相同/相似文本二次调用近乎瞬时（命中磁盘缓存）※ 磁盘缓存已于 v5.3 移除
; v5.2 变更（功能收敛）：
;  1. 移除错字检查（原 F8）功能，本工具专注语句润色
;  2. 后端同步仅保留 /polish 端点
; v5.3 变更（结构重构 + 去缓存）：
;  1. 目录重构为 dist/src/build/tools 四区；本脚本恒部署在 <部署根>\runtime\
;  2. 移除磁盘缓存（原 <部署根>\.cache），每次润色均直连云端 API：
;     重复调用不再瞬时、同句结果措辞可能不同，但消除了"改提示词忘升级
;     版本号导致一直吃旧结果"的隐患，磁盘上也不再有残留
; v5.4 变更（新增"手动选区润色"）：
;  1. 按热键时自动判别两种模式并共存：
;       · 用户已手动选中一段文字 → 选区模式：只润色该段，回填时只覆盖该段，
;         选区之外的其余内容保持完全不变
;       · 用户未选中任何文字 → 全选模式：沿用 v5.3 行为，润色整个输入框
;  2. 判别方式：先不按 ^a，直接发 ^c 试探。复制操作不会清除选区，因此
;     剪贴板能拿到非空文本即说明"当前存在选区"；拿不到则退回全选读取。
;     好处是选区模式下定位于原选区的信息被完整保留，回填只需 ^v。
;  3. 预览窗标题与提示行会标明当前处于哪种模式，避免误判时用户无感
;  4. [ui] select_mode 可强制指定：auto（默认，自动判别）/ all（总是全选）
;     —— 用于 VSCode 这类"无选区时按 Ctrl+C 会复制整行"的编辑器
; v5.5 变更（修复"改配置不生效"）：
;   1. 后端 -server 是分离启动的常驻进程，关掉 mosq-ai-fix.exe / 退出托盘
;      都不会结束它；而它一直用"启动那一刻"的配置 → 改完 app.ini 重启工具
;      仍会复用旧进程，日志里还是旧接口地址
;   2. 修复：OnExit 时请后端退出并按 PID 兜底回收；启动时先清掉遗留后端，
;      确保每次运行都是全新进程。后端侧另有配置热重载做双保险
; v5.5 变更（之二：统一程序显示名）：
;   1. 程序名收敛到唯一来源 AppName（脚本顶部 = "mosq-ai-fix"），托盘悬停提示、
;      弹窗标题、预览窗标题、通知标题全部取它；启动器 exe 的版本资源（app.rc）
;      同步写入同一个名字，Windows 的任务管理器/启动项/文件属性也随之一致
; v5.6 变更（新增"翻译成中文"，默认 F10）：
;   1. 新热键 translate_key（默认 F10）：选中文字后按它，把内容翻译成中文并
;      弹窗展示原文 + 译文；译文窗口只读，配「复制译文」按钮，**不提供替换原文**，
;      避免误覆盖（翻译的用途是看懂，不是改写）
;   2. 与润色共用选区判定逻辑 ReadInputText()：有选区翻选区，无选区翻整个输入框
;   3. 连通性自检：自检会探测 /translate 端点（POST 空文本，不消耗额度）
;   4. 两热键分别注册：某一个被占用时不影响另一个，并明确指出冲突项
;   5. 译文与原文等价（代码标识符/专有名词/已是中文）时，窗口用橙色文字说明，
;      避免"上下两块一模一样"被误读成翻译失败
;   6. 润色/翻译窗口的**标题栏追加实际使用的模型名**（方案 B）：
;      后端把响应体里的 model 字段经响应头 X-Model 回传，前端取到就拼成
;      「mosq-ai-fix · 预览（自动全选） — deepseek-flash」；取不到则不加，不显示"未知"
; =============================================================

; ---------------- 程序显示名（唯一来源，改这里即可全局生效）----------------
; 本工具在界面上出现的"程序名"统一取这一处：托盘悬停提示、各弹窗标题、
; 预览窗标题、通知标题、自检报告；Windows 侧（任务管理器/启动项/文件属性）
; 读的则是启动器 exe 的版本资源，见 src\launcher\app.rc 的
; FileDescription/ProductName —— 两处必须保持同一个名字，改名字要一起改。
; 当前取值刻意与启动器文件名保持一致（mosq-ai-fix），这样托盘、界面、
; 任务管理器、exe 文件名四者呈现同一名称，用户看到的"程序"只有一个名字。
; 说明：脚本自身的文件名仍是 typo_check.ahk（磁盘上的真实文件），
; 那属于实现细节，不作为对外显示名。
global AppName := "mosq-ai-fix"

; 目录结构（v5.3）：脚本固定部署在 <部署根>\runtime\，其上一级即部署根
;   <部署根>\config\app.ini   部署参数（用户可改）
;   <部署根>\runtime\         本脚本 + check_ai.exe + AutoHotkey64.exe + icon.ico
; 因此 BaseDir 恒为 A_ScriptDir 的上一级，不再区分源码版/编译版
global BaseDir := A_ScriptDir "\.."           ; 部署根（runtime 的上一级）
global CfgIni := BaseDir "\config\app.ini"    ; 部署参数文件
global PolishKey := LoadHotkey("polish_key", "F9")       ; [hotkey] polish_key，失败回退 F9
global TranslateKey := LoadHotkey("translate_key", "F10") ; [hotkey] translate_key，失败回退 F10
global SelectMode := LoadSelectMode()  ; [ui] select_mode 读取，缺省 auto（自动判别选区/全选）
global ServerPort := "18765"           ; 常驻服务端口（与 check_ai.go 默认一致，可被 [server] port 覆盖）
try {
    p := Trim(IniRead(CfgIni, "server", "port"))
    if p != ""
        ServerPort := p
} catch {
}
global ServerPid := 0                  ; 本前端拉起的后端进程号（退出时用它回收）

; 退出时回收后端。放在最靠前的位置，连自检模式（-selftest）退出也能生效，
; 否则自检拉起的常驻服务会一直留在后台、占着端口并锁住 check_ai.exe。
OnExit(ShutdownBackend)

; 托盘图标：同目录的 icon.ico（源码版与 Ahk2Exe 编译版路径一致）
icoFile := A_ScriptDir "\icon.ico"
if FileExist(icoFile)
    TraySetIcon(icoFile)

; 托盘悬停提示（就是截图里那行名字）。不显式设置时 AHK 会用脚本文件名兜底，
; 这里统一为 AppName，确保托盘与弹窗标题、Windows 程序名完全一致。
A_IconTip := AppName

; ---------------- 自检模式 ----------------
; 调用：runtime\AutoHotkey64.exe runtime\typo_check.ahk -selftest
;   （须从 runtime\ 下以相对路径调用，或用绝对路径；结果直接打印到控制台）
; 说明：mosq-ai-fix.exe 为 GUI 子系统且不转发参数，故不能用它跑自检。
; 输出内容：配置读取结果 + 一次真实润色调用，用于验证部署完整性。
; 报告同时写入文件（权威，路径见下）与控制台（尽力而为）：
;   文件 %TEMP%\mosq_selftest.txt    —— build\build.ps1 读它来判定自检结果
if A_Args.Length > 0 && A_Args[1] = "-selftest" {
    RunSelfTest()
    ExitApp()
}

RunSelfTest() {
    global SelfTestReport, SelectMode, PolishKey, TranslateKey, AppName
    rep := ""
    rep .= "程序显示名: " AppName "`n"
    rep .= "AI 校对: " (IsAIEnabled() ? "已启用" : "未启用（编辑 config\app.ini 配置 API Key 后启用）") "`n"
    rep .= "润色热键: " PolishKey "（改 config\app.ini 的 [hotkey] polish_key 后重启生效）`n"
    rep .= "翻译热键: " TranslateKey "（改 config\app.ini 的 [hotkey] translate_key 后重启生效）`n"
    rep .= "润色模式: " (SelectMode = "all" ? "总是全选（[ui] select_mode=all）" : "自动判别选区/全选（[ui] select_mode=auto）") "`n"
    rep .= "右下角提醒: " (TrayTipEnabled() ? "开启（[ui] tray_tip=true）" : "关闭（[ui] tray_tip=false）") "`n"
    ; 润色自检：结果不稳定，只验证调用成功且输出非空
    p := RunPolish("我今天真的挺想去的，但是时间上面好像有点不太够。")
    rep .= "润色状态: " p[1] "，结果长度 " StrLen(p[2]) " 字`n"
    if p[2] != ""
        rep .= "  " p[2] "`n"
    ; 实际模型来自响应头 X-Model（界面标题栏展示的就是它），顺带验证这条链路通了
    rep .= "实际模型: " (Trim(p[3]) = "" ? "未返回（后端过旧或上游未回 model 字段）" : p[3]) "`n"
    ; 翻译只探测端点是否就绪（POST 空文本，后端不调用大模型、不消耗额度）
    rep .= "翻译端点: " (EndpointReady("translate") ? "可用（/translate 已就绪）" : "不可用（后端过旧或未启动，请重新构建部署）") "`n"

    ; 必须写文件：GUI 子系统的 stdout 句柄在部分宿主下不可用（如 PowerShell 的 & 调用），
    ; 只写 stdout 会让自检结果不可见、也无法被构建脚本判定。
    SelfTestReport := A_Temp "\mosq_selftest.txt"
    try FileAppend(rep, SelfTestReport, "UTF-8")
    try FileAppend(rep, "*")
}

; ---------------- 焦点可编辑检测 ----------------
; 判断当前活动窗口的焦点控件是否可编辑文本
; 白名单放行已知编辑控件；黑名单拦截明显不可编辑控件；其余未知类型放行避免误伤
; Electron/Chromium 应用（VSCode、Typora、新版微信、Chrome 等）的渲染进程控件
; 无法被枚举，ControlGetFocus 常返回空——此时看窗口类放行，避免误拦
IsEditableFocused() {
    try {
        focusCtrl := ControlGetFocus("A")
        if focusCtrl = "" {
            winClass := WinGetClass("A")
            if InStr(winClass, "Chrome_WidgetWin")
                return true
            return false
        }
        ; AHK v2.0 无 ControlGetType（v2.1 才引入），用 WinGetClass 取控件类名
        ctrlType := WinGetClass("ahk_id " focusCtrl)
        ; 明确可编辑：标准编辑框/RichEdit(微信、记事本)/Scintilla(编辑器)/Chromium渲染框(新版微信、Electron、浏览器)
        if InStr(ctrlType, "edit") || InStr(ctrlType, "scintilla") || InStr(ctrlType, "chrome_renderwidget")
            return true
        ; 明显不可编辑：按钮/列表/树/静态文本/工具条/标签页
        if InStr(ctrlType, "button") || InStr(ctrlType, "listview") || InStr(ctrlType, "listbox")
            || InStr(ctrlType, "treeview") || InStr(ctrlType, "static") || InStr(ctrlType, "toolbar")
            || InStr(ctrlType, "statusbar") || InStr(ctrlType, "tab")
            return false
        ; 其余类型（含未知自绘编辑器）放行，避免误伤
        return true
    } catch
        return true   ; 检测失败时放行（用户主动按热键触发，宁可放过不可误拦）
}

; ---------------- 触发热键读取 ----------------
; 从 config\app.ini 的 [hotkey] 段读取热键（如 F9、F10、^F9、!F9）；
; 读取失败或为空时回退到 fallback。
;   polish_key    润色热键，默认 F9
;   translate_key 翻译成中文热键，默认 F10（v5.6 新增）
LoadHotkey(keyName, fallback) {
    global CfgIni
    try {
        k := Trim(IniRead(CfgIni, "hotkey", keyName))
        if k != ""
            return k
    }
    return fallback
}

; ---------------- 润色模式偏好（v5.4）----------------
; 从 config\app.ini 的 [ui] select_mode 读取按热键时的模式判别策略：
;   auto（默认）=自动判别：检测到选区则只润色选区，否则润色整个输入框
;   all         =总是全选整个输入框（v5.3 及以前的旧行为）
; 何时需要改：在 VSCode 等"光标处无选中时按 Ctrl+C 会复制整行"的编辑器里，
; 自动判别可能把"整行"误判成选区；此时设为 all 即可恢复"永远润色全文"。
; 取值非法或读取失败一律回退 auto。
LoadSelectMode() {
    global CfgIni
    try {
        v := Trim(IniRead(CfgIni, "ui", "select_mode"))
        if v = "all"
            return "all"
    }
    return "auto"
}

; ---------------- 右下角提醒开关 ----------------
; 从 config\app.ini 的 [ui] tray_tip 读取：true=一键修正后在右下角弹
; "已修正并回填"提醒；false=不弹（默认，读取失败也视为 false）
TrayTipEnabled() {
    global CfgIni
    try {
        v := Trim(IniRead(CfgIni, "ui", "tray_tip"))
        return v = "true" || v = "1"
    }
    return false
}

; ---------------- AI 校对配置检查 ----------------
IsAIEnabled() {
    global CfgIni
    if !FileExist(CfgIni)
        return false
    try {
        if IniRead(CfgIni, "ai", "enabled") != "true"
            return false
        if IniRead(CfgIni, "ai", "api_key") = ""
            return false
        return true
    } catch
        return false
}

; ---------------- 常驻服务（P0 性能优化：进程仅启动一次，复用连接与缓存）----------------
; 后端 check_ai.exe -server 启动后常驻 127.0.0.1:ServerPort，
; 前端通过 HTTP 调用 /polish，免去每次调用都冷启动进程的开销，
; 同时复用 Go 端的连接池与磁盘缓存（相同/相似文本二次调用近乎瞬时）。

; 确保本地润色服务已启动：已运行则直接返回；未运行则拉起 check_ai.exe -server 并等待就绪
EnsureServer() {
    global BaseDir, ServerPort, ServerPid
    if IsServerUp()
        return true
    exePath := A_ScriptDir "\check_ai.exe"
    if !FileExist(exePath)
        return false
    ServerPid := Run('"' exePath '" -server', BaseDir, "Hide")   ; 记下 PID，退出时回收
    loop 60 {                     ; 最多等 6 秒让它监听端口
        Sleep(100)
        if IsServerUp()
            return true
    }
    return false
}

; ---------------- 后端进程回收（v5.5）----------------
; 后端由前端以 Run(..., "Hide") **分离**启动，并非前端的子进程，因此
; "关闭 mosq-ai-fix.exe" 或"从托盘退出"都不会让它结束。历史上它就长期驻留，
; 并始终使用**自己启动那一刻**读到的配置，于是出现：
;   关闭工具 → 改 app.ini 换接口地址 → 重新启动 → 前端探测到端口有人应答
;   就直接复用这个旧进程 → 日志里仍是旧地址。
; 这里两道保险：
;   ① ShutdownBackend（OnExit 注册）：退出时请后端自己退出，再按 PID 兜底强杀
;   ② StopLeftoverServer（启动时调用）：先清掉任何遗留后端，保证本次是全新进程
;      （顺带解决"旧进程锁住 check_ai.exe 导致无法重新构建/升级"）
ShutdownBackend(*) {
    global ServerPid
    PostShutdown()
    ; 按 PID 强杀前先确认该 PID 仍是 check_ai.exe，避免 PID 被系统复用后误杀
    if ServerPid && ProcessExist(ServerPid) {
        try {
            if ProcessGetName(ServerPid) = "check_ai.exe"
                ProcessClose(ServerPid)
        }
    }
    ServerPid := 0
}

; 启动时清掉遗留后端（若本来没在跑，POST 会被拒绝，静默忽略即可）
StopLeftoverServer() {
    PostShutdown()
    Sleep(250)      ; 留一点时间让它释放端口
}

; 请后端优雅退出：POST /shutdown（失败静默 —— 服务没在跑属正常情况）
PostShutdown() {
    global ServerPort
    try {
        http := ComObject("WinHttp.WinHttpRequest.5.1")
        http.Open("POST", "http://127.0.0.1:" ServerPort "/shutdown", false)
        http.SetTimeouts(400, 400, 600, 600)
        http.Send("")
    } catch {
    }
}

; 探测服务是否就绪：向 /polish 发空请求，期望 200（空文本服务返回空串）
IsServerUp() {
    global ServerPort
    try {
        http := ComObject("WinHttp.WinHttpRequest.5.1")
        http.Open("POST", "http://127.0.0.1:" ServerPort "/polish", false)
        http.SetRequestHeader("Content-Type", "text/plain; charset=utf-8")
        http.SetTimeouts(1000, 1000, 1000, 1000)    ; 探测用短超时（resolve/connect/send/receive）
        http.Send(" ")                         ; 发一个空格（空串 Send 在部分环境会报错）
        return (http.Status = 200)
    } catch {
        return false
    }
}

; 调用本地服务端点，返回 [body, model]
;   body  : UTF-8 解码后的响应文本
;   model : 响应头 X-Model —— 后端回报的"实际服务本次请求的模型名"（取不到为空串）
;           注意它可能被上游/中转改写，与 app.ini 里填的 model 不一定相同
HttpPost(endpoint, text) {
    global ServerPort
    http := ComObject("WinHttp.WinHttpRequest.5.1")
    http.Open("POST", "http://127.0.0.1:" ServerPort "/" endpoint, false)
    http.SetRequestHeader("Content-Type", "text/plain; charset=utf-8")
    http.SetTimeouts(3000, 3000, 30000, 30000)        ; resolve3s / connect3s / send30s / receive30s
    ; 以 UTF-8 字节发送，避免中文被当成 ANSI/BSTR 丢失
    stream := ComObject("ADODB.Stream")
    stream.Type := 2                           ; adTypeText
    stream.Charset := "utf-8"
    stream.Open()
    stream.WriteText(text)
    stream.Position := 0
    stream.Type := 1                           ; adTypeBinary
    http.Send(stream.Read())
    if (http.Status != 200)
        throw Error("HTTP " http.Status)
    model := ""
    try model := Trim(http.GetResponseHeader("X-Model"))   ; 头不存在时返回空串
    return [BytesToUtf8(http.ResponseBody), model]
}

; 将 WinHttpRequest 的二进制响应体按 UTF-8 正确解码为字符串（避免中文乱码）
BytesToUtf8(body) {
    stream := ComObject("ADODB.Stream")
    stream.Type := 1                           ; adTypeBinary
    stream.Open()
    stream.Write(body)
    stream.Position := 0
    stream.Type := 2                           ; adTypeText
    stream.Charset := "utf-8"
    return stream.ReadText()
}

; ---------------- 调用本地常驻服务（v5.0；v5.6 起润色/翻译共用）----------------
; 返回 [status, result, model]
;   status : "ok" 正常 | "no_key" 未配置 Key | "no_python" 未能启动程序 | "error" 调用失败
;   result : 处理后的整段文本（ok 时）
;   model  : 实际使用的模型名（ok 时可能为空串，供界面标题展示）
; endpoint: "polish" 润色 | "translate" 翻译成中文
RunEndpoint(endpoint, text) {
    ; 1. 确保常驻服务已启动
    if !EnsureServer()
        return ["no_python", "", ""]

    ; 2. 调用端点；若服务异常则尝试重启一次再调用
    try {
        r := HttpPost(endpoint, text)
    } catch {
        if !EnsureServer() {
            return ["error", "", ""]
        }
        try {
            r := HttpPost(endpoint, text)
        } catch {
            return ["error", "", ""]
        }
    }

    content := Trim(r[1], "`r`n")
    if SubStr(content, 1, 2) = "__" {
        if InStr(content, "NO_KEY")
            return ["no_key", "", ""]
        return ["error", "", ""]
    }
    return ["ok", content, r[2]]
}

; 润色（F9）
RunPolish(text) {
    return RunEndpoint("polish", text)
}

; 翻译成中文（F10）
RunTranslate(text) {
    return RunEndpoint("translate", text)
}

; 端点连通性探测：POST 空文本（后端对空文本直接返回空串，不会调用大模型，
; 因此不消耗额度）。用于自检确认 /translate 之类的端点确实存在（否则会 404 抛错）。
EndpointReady(endpoint) {
    try {
        HttpPost(endpoint, "")
        return true
    } catch {
        return false
    }
}

; ---------------- 注册热键（v5.6：润色 + 翻译两个）----------------
; 两个热键各自独立注册：某一个被别的程序占用时，不影响另一个继续可用，
; 但会明确告知是哪一个冲突、怎么改，避免"按了没反应"的困惑。
hotkeyErrs := ""
if PolishKey = TranslateKey {
    MsgBox("配置错误：[hotkey] 段的 polish_key 与 translate_key 不能相同（当前都是 " PolishKey "）。请用记事本打开 config\app.ini 改掉其中一个，保存后重新双击「mosq-ai-fix.exe」。", AppName, "Iconi")
    ExitApp()
}
try {
    Hotkey(PolishKey, PolishText)
} catch {
    hotkeyErrs .= PolishKey "（润色 polish_key） "
}
try {
    Hotkey(TranslateKey, TranslateText)
} catch {
    hotkeyErrs .= TranslateKey "（翻译 translate_key） "
}
if hotkeyErrs != "" {
    MsgBox("以下热键已被其他程序占用：" Trim(hotkeyErrs) "`n`n请用记事本打开 config\app.ini，修改 [hotkey] 段里对应的按键，保存后重新双击「mosq-ai-fix.exe」。`n`n格式示例：F9、F10、^F9(Ctrl+F9)、!F9(Alt+F9)、+F9(Shift+F9)", AppName, "Iconi")
    ExitApp()
}

; 启动时先清掉上一次残留的后端进程（详见 ShutdownBackend 注释）：
; 保证本次运行使用全新进程 —— 新配置、新二进制，绝不复用旧进程内的旧配置。
StopLeftoverServer()

if IsAIEnabled()
    TrayTip("已运行（AI 已开启）｜" PolishKey " 润色 · " TranslateKey " 翻译成中文", AppName, 3)
else
    TrayTip("已运行 · AI 未启用：编辑 config\app.ini 填入 API Key 后重启", AppName, 5)

; ---------------- 读取输入框文本并判别润色模式（v5.4）----------------
; 返回 [mode, text]
;   mode = "sel"  手动选区模式：只润色 text（= 用户选中的那段文字）
;   mode = "all"  全选模式：text = 整个输入框的内容
;   mode = "none" 没读到任何文字
;
; 判别原理（两种模式共存的关键）：
;   ① 先"试探性复制"：不按 ^a，直接 Send("^c")。
;      · 有选区 → 剪贴板拿到选区文本 → 判定 sel；
;      · 无选区 → 该应用不会往剪贴板写任何东西，ClipWait 超时 → 判定 all。
;   ② 关键前提：Ctrl+C 只复制、不清除选区。所以 sel 分支结束后，
;      目标输入框里的那段选区依然完好，回填时直接 ^v 就能"只覆盖这一段"，
;      无需知道选区的起止偏移量。若这里改成先 ^a 再判断，选区会被摧毁且
;      无法复原（任意应用里都拿不到偏移量），因此顺序不可颠倒。
;   ③ 之后才按 ^a 读全文——这一步只发生在 all 分支（回到 v5.3 旧行为）。
ReadInputText() {
    global SelectMode
    saved := ClipboardAll()

    ; ① 试探选区（select_mode=all 时跳过，直接走全选）
    if SelectMode = "auto" {
        A_Clipboard := ""
        Send("^c")
        if ClipWait(0.5) {
            sel := A_Clipboard
            if Trim(sel) != "" {
                A_Clipboard := saved
                return ["sel", sel]
            }
        }
    }

    ; ② 未检测到选区 → 全选读取整个输入框
    A_Clipboard := ""
    Send("^a")
    Sleep(80)
    Send("^c")
    if !ClipWait(0.8) {
        A_Clipboard := saved
        return ["none", ""]
    }
    text := A_Clipboard
    A_Clipboard := saved
    return ["all", text]
}

; ---------------- 主流程：语句润色（v5.0，默认 F9）----------------
; v5.4 起自动区分"手动选区"与"自动全选"两种模式，见 ReadInputText()
PolishText(*) {
    global PolishKey
    ; 通用模式：任意可编辑输入框都能润色（微信/浏览器/记事本/编辑器等）
    if !IsEditableFocused() {
        MsgBox("当前焦点不在文本输入框里，请先点击要润色的输入框（微信聊天框、网页文本框、记事本等均可），再按 " PolishKey, AppName, "Iconi")
        return
    }

    ; 1. 读取文本并判定模式：有选区读选区，无选区读全文
    r := ReadInputText()
    mode := r[1]
    text := r[2]

    if mode = "none" {
        ShowAutoCloseTip("没读到文字", "请先点击输入框，再按 " . PolishKey, 1000)
        return
    }
    if Trim(text) = "" {
        ShowAutoCloseTip("没有可润色的文字", "先在输入框里输入内容，再按 " . PolishKey, 1000)
        return
    }

    ; 2. 检查 AI 配置
    if !IsAIEnabled() {
        MsgBox("AI 校对未启用。请用记事本打开 config\app.ini，填入云端 API Key 并设 enabled=true", AppName, "Iconi")
        return
    }

    ; 3. 调用云端 AI 润色（约 1-5 秒，鼠标旁会显示"润色中"提示）
    ToolTip("正在润色，请稍候…")
    p := RunPolish(text)
    ToolTip()

    if p[1] = "no_key" {
        MsgBox("未检测到 API Key。请用记事本打开 config\app.ini 填入", AppName, "Iconi")
        return
    }
    if p[1] = "no_python" {
        MsgBox("未能启动校对程序 check_ai.exe。请确认工具目录里有 check_ai.exe（Go 编译，无需安装 Python），且未被杀毒软件拦截；仍不行请重新解压/复制整个工具目录。", AppName, "Iconi")
        return
    }
    if p[1] = "error" {
        MsgBox("AI 调用失败，请检查网络连接后重试；如超时可把 config\app.ini 里的 timeout 调大（当前 15 秒）", AppName, "Iconi")
        return
    }

    ; 4. 弹窗预览润色结果（可编辑微调），确认后按原模式替换
    ShowPolishGui(text, p[2], WinGetID("A"), mode, p[3])
}

; ---------------- 主流程：翻译成中文（v5.6，默认 F10）----------------
; 与润色共用 ReadInputText() 的选区判定：选中了文字就翻这一段，
; 没选中就把整个输入框的内容当原文（窗口里会写明翻的是哪一段）。
; 与润色的关键区别：翻译**只展示、不改动原文**——译文窗口是只读的，
; 另配一个「复制译文」按钮，避免误操作把原文覆盖掉。
TranslateText(*) {
    global TranslateKey
    if !IsEditableFocused() {
        MsgBox("当前焦点不在文本输入框里，请先点击要翻译的输入框（微信聊天框、网页文本框、记事本等均可），再按 " TranslateKey, AppName, "Iconi")
        return
    }

    ; 1. 读取文本并判定模式：有选区读选区，无选区读全文
    r := ReadInputText()
    mode := r[1]
    text := r[2]

    if mode = "none" {
        ShowAutoCloseTip("没读到文字", "请先选中要翻译的文字，再按 " . TranslateKey, 1000)
        return
    }
    if Trim(text) = "" {
        ShowAutoCloseTip("没有可翻译的文字", "先选中要翻译的文字，再按 " . TranslateKey, 1000)
        return
    }

    ; 2. 检查 AI 配置
    if !IsAIEnabled() {
        MsgBox("AI 未启用。请用记事本打开 config\app.ini，填入云端 API Key 并设 enabled=true", AppName, "Iconi")
        return
    }

    ; 3. 调用云端 AI 翻译（约 1-5 秒，鼠标旁会显示"正在翻译"提示）
    ToolTip("正在翻译，请稍候…")
    p := RunTranslate(text)
    ToolTip()

    if p[1] = "no_key" {
        MsgBox("未检测到 API Key。请用记事本打开 config\app.ini 填入", AppName, "Iconi")
        return
    }
    if p[1] = "no_python" {
        MsgBox("未能启动后端程序 check_ai.exe。请确认工具目录里有 check_ai.exe（Go 编译，无需安装 Python），且未被杀毒软件拦截；仍不行请重新解压/复制整个工具目录。", AppName, "Iconi")
        return
    }
    if p[1] = "error" {
        MsgBox("翻译失败，请检查网络连接后重试；如超时可把 config\app.ini 里的 timeout 调大", AppName, "Iconi")
        return
    }

    ; 4. 展示译文（只读，不影响原文）
    ShowTranslateGui(text, p[2], mode, p[3])
}

; ---------------- 译文窗口（v5.6）----------------
; 上下两块：上为原文（只读、灰色，便于对照），下为中文译文（只读、可选中复制）。
; 刻意**不提供"替换原文"**：翻译的用途是"看懂"，误覆盖原文的代价太高；
; 需要回填时点「复制译文」再自行粘贴即可。
; model = 实际使用的模型名（响应头 X-Model），为空则标题不追加。
ShowTranslateGui(orig, translated, mode, model := "") {
    scopeText := (mode = "sel") ? "已选中的 " StrLen(orig) " 字" : "整个输入框（未检测到选区）"

    ; 译文与原文等价时给出明确说明。否则界面上下两块内容一模一样，
    ; 用户会以为"翻译没生效"（实际是代码标识符/专有名词/已是中文这类无需翻译的内容，
    ; 模型按规则保留了原文——行为正确，但必须让用户看得出来）。
    sameAsSource := NormForCompare(translated) = NormForCompare(orig)

    myGui := Gui("+AlwaysOnTop", TitleWithModel(AppName " · 翻译成中文", model))
    myGui.SetFont("s10", "Microsoft YaHei")

    myGui.SetFont("s9 norm")
    myGui.Add("Text", "w620 c6B7280", "原文（" scopeText "）")
    myGui.SetFont("s10")
    myGui.Add("Edit", "w620 h90 ReadOnly", orig)

    myGui.SetFont("s9 norm")
    myGui.Add("Text", "w620 c6B7280", "中文译文（可选中复制；如需回填原文，点「复制译文」后自行粘贴）")
    if sameAsSource
        myGui.Add("Text", "w620 cB45309", "注意：译文与原文完全相同。该内容通常无需翻译（代码标识符、专有名词、符号数字，或本身已是中文），工具按规则原样返回，并非失败。")
    myGui.SetFont("s10")
    trans := myGui.Add("Edit", "w620 h220 ReadOnly", translated)

    btnCopy := myGui.Add("Button", "w130 h32 Default", "复制译文")
    btnClose := myGui.Add("Button", "x+12 w100 h32", "关闭")
    btnCopy.OnEvent("Click", (*) => CopyTranslation(myGui, trans))
    btnClose.OnEvent("Click", (*) => myGui.Destroy())
    myGui.Show()
}

; 归一化后比较：去掉首尾空白与 BOM（从文件复制来的文本常带 U+FEFF），
; 用于判断"译文是否与原文等价"，避免把"去掉了 BOM"误判成"翻译过了"。
NormForCompare(s) {
    s := StrReplace(s, Chr(0xFEFF), "")
    return Trim(s)
}

; 窗口标题：在基础标题后追加实际使用的模型名（方案 B：标题栏展示）。
;   base  = 如 "mosq-ai-fix · 预览（自动全选）"
;   model = 响应头 X-Model 的值；为空（老后端 / 未返回）时不追加，避免出现"未知"之类的噪音
TitleWithModel(base, model) {
    m := Trim(model)
    if m = ""
        return base
    return base " — " m
}

; 把译文写入剪贴板（保留原有剪贴板内容不做恢复：用户点"复制"就是要用它）
CopyTranslation(myGui, transCtrl) {
    text := transCtrl.Text
    if Trim(text) = "" {
        ShowAutoCloseTip("译文为空", "没有可复制的内容", 1000)
        return
    }
    A_Clipboard := text
    myGui.Destroy()
    TrayTip("译文已复制到剪贴板", AppName, 3)
}

; ---------------- 润色结果弹窗（v5.0；v5.4 起标注模式；v5.6 起标题附实际模型）----------------
; 展示润色后的文本，Edit 可直接编辑微调；点"替换原文"按 mode 回填
;   mode  = "sel" → 只覆盖原选区；"all" → 覆盖整个输入框
;   model = 实际使用的模型名（来自响应头 X-Model）；为空则标题不追加，避免噪音
ShowPolishGui(orig, polished, targetHwnd, mode, model := "") {
    modeName := (mode = "sel") ? "手动选区" : "自动全选"
    if mode = "sel"
        modeHint := "手动选区模式：已选中 " StrLen(orig) " 字。点「替换原文」只覆盖选中的这一段，选区之外的文字保持不动。"
    else
        modeHint := "自动全选模式：未检测到选区，点「替换原文」将替换整个输入框的内容。"

    myGui := Gui("+AlwaysOnTop", TitleWithModel(AppName " · 预览（" modeName "）", model))
    myGui.SetFont("s10", "Microsoft YaHei")
    myGui.Add("Text", "w560", "润色结果（可直接编辑微调）：")
    myGui.SetFont("s9 norm")
    myGui.Add("Text", "w560 c6B7280", modeHint)
    myGui.SetFont("s10")
    edit := myGui.Add("Edit", "w560 h200 WantTab", polished)

    btnApply := myGui.Add("Button", "w130 h32 Default", "替换原文")
    btnClose := myGui.Add("Button", "x+12 w100 h32", "取消")
    btnApply.OnEvent("Click", (*) => ApplyPolish(myGui, edit, targetHwnd, mode))
    btnClose.OnEvent("Click", (*) => myGui.Destroy())
    myGui.Show()
}

; ---------------- 润色替换回填（v5.0；v5.4 起区分模式）----------------
ApplyPolish(myGui, editCtrl, targetHwnd, mode) {
    newText := editCtrl.Text
    if Trim(newText) = "" {
        MsgBox("润色结果为空，无法替换。请在弹窗里手动编辑内容后再点「替换原文」，或点「取消」。", AppName, "Iconi")
        return
    }

    saved := ClipboardAll()
    A_Clipboard := newText
    myGui.Hide()               ; 先隐藏弹窗，露出原窗口
    WinActivate(targetHwnd)    ; 把焦点还给原输入框（关键修复）
    Sleep(150)                 ; 等窗口激活完成
    if mode = "sel" {
        ; 选区模式：读取阶段只发过 ^c，原选区未被破坏，直接粘贴就会
        ; "用润色结果覆盖选区本身"，选区之外的文字一律不动。
        ; 注意此处绝不能发 ^a，否则会变成整篇替换。
        Send("^v")
    } else {
        Send("^a")
        Sleep(80)
        Send("^v")
    }
    Sleep(120)
    A_Clipboard := saved
    myGui.Destroy()
    ; 右下角提醒可按配置关闭（[ui] tray_tip=false 时不弹）
    if TrayTipEnabled()
        TrayTip("润色完成 · " . ((mode = "sel") ? "已替换选中的部分" : "已替换为润色后的文本"), AppName, 3)
}

; ---------------- 轻提示（主题卡片 + 淡入淡出，约 1 秒后自动关闭） ----------------
; 输入框无文字 / 没读到文字 等轻量场景的提示：白色卡片 + 左侧主题色条 + 图标 +
; 主副标题两行，Win11 下自动圆角；淡入显示、durationMs 后淡出销毁；
; 不抢焦点、无需用户点击
; 注意：必须用全局引用 TipGuiRef 持有 Gui 对象——AHK v2 中若引用计数归零，
;       窗口会被自动销毁，导致淡出定时器访问 Hwnd 时抛 "Gui has no window"
ShowAutoCloseTip(title, subtitle, durationMs := 1000) {
    global TipGuiRef
    ; 蓝色主题条 + ℹ 图标（提示语义）
    barColor := "3B82F6"
    iconColor := "3B82F6"
    iconChar := "ℹ"
    tipGui := Gui("+AlwaysOnTop -Caption +ToolWindow +E0x08000000", AppName)
    tipGui.BackColor := "FFFFFF"
    tipGui.MarginX := 0
    tipGui.MarginY := 0
    ; Win11 圆角（Win10 下不生效则为直角白卡，无碍）
    try {
        corner := 2
        DllCall("dwmapi.dll\DwmSetWindowAttribute", "Ptr", tipGui.Hwnd, "UInt", 33, "Ptr", &corner, "UInt", 4)
    }
    ; 左侧 5px 主题色条
    tipGui.Add("Text", "x0 y0 w5 h68 Background" . barColor)
    ; 其余三边 1px 浅灰描边（顶/底/右），圆角处自然过渡
    tipGui.Add("Text", "x5 y0 w415 h1 BackgroundE5E7EB")
    tipGui.Add("Text", "x5 y67 w415 h1 BackgroundE5E7EB")
    tipGui.Add("Text", "x419 y0 w1 h68 BackgroundE5E7EB")
    ; 主题图标
    tipGui.SetFont("s18 bold", "Segoe UI Symbol")
    tipGui.Add("Text", "x18 y15 w30 h38 BackgroundFFFFFF c" . iconColor, iconChar)
    ; 主标题 + 副标题（左对齐两行）
    tipGui.SetFont("s12 bold", "Microsoft YaHei")
    tipGui.Add("Text", "x56 y13 w350 h26 BackgroundFFFFFF c111827", title)
    tipGui.SetFont("s10 norm", "Microsoft YaHei")
    tipGui.Add("Text", "x56 y41 w350 h20 BackgroundFFFFFF c6B7280", subtitle)
    tipGui.Show("Center w420 h68")
    ; 淡入（约 0.12s）→ 停留 → 淡出（约 0.18s）销毁
    WinSetTransparent(0, "ahk_id " tipGui.Hwnd)
    TipGuiRef := tipGui                        ; 关键：全局引用保活，防止窗口被回收
    SetTimer(FadeTip.Bind(tipGui, 1), -10)
    SetTimer(FadeTip.Bind(tipGui, 0), durationMs)
    return tipGui
}

; 轻提示动画：dir=1 淡入（0→255），dir=0 淡出（255→0）后销毁
; 所有对 Hwnd 的操作都加 try 兜底：窗口若已被系统/用户关闭，静默清理而不报错
FadeTip(guiObj, dir, step := 0) {
    global TipGuiRef
    if dir = 1 {
        alpha := Min(255, step * 32)
        if alpha >= 255 {
            try
                WinSetTransparent(255, "ahk_id " guiObj.Hwnd)
            catch {
                TipGuiRef := ""
                guiObj.Destroy()
            }
            return
        }
        try
            WinSetTransparent(alpha, "ahk_id " guiObj.Hwnd)
        catch {
            TipGuiRef := ""
            guiObj.Destroy()
            return
        }
        SetTimer(FadeTip.Bind(guiObj, 1, step + 1), -15)
    } else {
        alpha := 255 - step * 40
        if alpha <= 0 {
            TipGuiRef := ""
            guiObj.Destroy()
            return
        }
        try
            WinSetTransparent(alpha, "ahk_id " guiObj.Hwnd)
        catch {
            TipGuiRef := ""
            guiObj.Destroy()
            return
        }
        SetTimer(FadeTip.Bind(guiObj, 0, step + 1), -25)
    }
}

; ---------------- 语句润色 · 完 ----------------
