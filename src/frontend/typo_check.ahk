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
; 文本润色工具  v5.2（Go 版 · 免 Python 环境）
; 用法：在任意可编辑的输入框（微信聊天框、网页文本框、
;       记事本、代码编辑器等）打好字后：
;         按 F9 润色当前语句，改写得更得体、通顺、易理解，
;             弹窗预览（可编辑微调），点"替换原文"回填
;       热键可在 config\app.ini 的 [hotkey] 段修改（polish_key）
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
;     免去每次调用都冷启动进程的开销，并复用连接池与磁盘缓存
;  2. 相同/相似文本二次调用近乎瞬时（命中磁盘缓存）
; v5.2 变更（功能收敛）：
;  1. 移除错字检查（原 F8）功能，本工具专注语句润色
;  2. 后端同步仅保留 /polish 端点
; =============================================================

; 目录结构（v5.3）：脚本固定部署在 <部署根>\runtime\，其上一级即部署根
;   <部署根>\config\app.ini   部署参数（用户可改）
;   <部署根>\runtime\         本脚本 + check_ai.exe + AutoHotkey64.exe + icon.ico
; 因此 BaseDir 恒为 A_ScriptDir 的上一级，不再区分源码版/编译版
global BaseDir := A_ScriptDir "\.."           ; 部署根（runtime 的上一级）
global CfgIni := BaseDir "\config\app.ini"    ; 部署参数文件
global PolishKey := LoadPolishHotkey() ; [hotkey] polish_key 读取，失败回退 F9
global ServerPort := "18765"           ; 常驻服务端口（与 check_ai.go 默认一致，可被 [server] port 覆盖）
try {
    p := Trim(IniRead(CfgIni, "server", "port"))
    if p != ""
        ServerPort := p
} catch {
}

; 托盘图标：同目录的 icon.ico（源码版与 Ahk2Exe 编译版路径一致）
icoFile := A_ScriptDir "\icon.ico"
if FileExist(icoFile)
    TraySetIcon(icoFile)

; ---------------- 自检模式 ----------------
; 调用：runtime\AutoHotkey64.exe runtime\typo_check.ahk -selftest
;   （须从 runtime\ 下以相对路径调用，或用绝对路径；结果直接打印到控制台）
; 说明：mosq-ai-fix.exe 为 GUI 子系统且不转发参数，故不能用它跑自检。
; 输出内容：配置读取结果 + 一次真实润色调用，用于验证部署完整性。
if A_Args.Length > 0 && A_Args[1] = "-selftest" {
    RunSelfTest()
    ExitApp()
}

RunSelfTest() {
    FileAppend("AI 校对: " (IsAIEnabled() ? "已启用" : "未启用（编辑 config\app.ini 配置 API Key 后启用）") "`n", "*")
    FileAppend("润色热键: " PolishKey "（改 config\app.ini 的 [hotkey] polish_key 后重启生效）`n", "*")
    FileAppend("右下角提醒: " (TrayTipEnabled() ? "开启（[ui] tray_tip=true）" : "关闭（[ui] tray_tip=false）") "`n", "*")
    ; 润色自检：结果不稳定，只验证调用成功且输出非空
    p := RunPolish("我今天真的挺想去的，但是时间上面好像有点不太够。")
    FileAppend("润色状态: " p[1] "，结果长度 " StrLen(p[2]) " 字`n", "*")
    if p[2] != ""
        FileAppend("  " p[2] "`n", "*")
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
; 从 config\app.ini 的 [hotkey] polish_key 读取润色热键（如 F9、^F9、!F9），读取失败/为空回退 F9
LoadPolishHotkey() {
    global CfgIni
    try {
        k := Trim(IniRead(CfgIni, "hotkey", "polish_key"))
        if k != ""
            return k
    }
    return "F9"
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
    global BaseDir, ServerPort
    if IsServerUp()
        return true
    exePath := A_ScriptDir "\check_ai.exe"
    if !FileExist(exePath)
        return false
    Run('"' exePath '" -server', BaseDir, "Hide")
    loop 60 {                     ; 最多等 6 秒让它监听端口
        Sleep(100)
        if IsServerUp()
            return true
    }
    return false
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

; 调用本地服务端点，返回 UTF-8 解码后的响应文本
; endpoint: 端点名（v5.2 起仅 "polish"）
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
    return BytesToUtf8(http.ResponseBody)
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

; ---------------- 调用本地常驻服务做语句润色（v5.0）----------------
; 返回 [status, polishedText]
;   status : "ok" 正常 | "no_key" 未配置 Key | "no_python" 未能启动程序 | "error" 调用失败
;   text   : 润色后的整段文本（ok 时）
RunPolish(text) {
    ; 1. 确保常驻服务已启动
    if !EnsureServer()
        return ["no_python", ""]

    ; 2. 调用 /polish；若服务异常则尝试重启一次再调用
    try {
        content := HttpPost("polish", text)
    } catch {
        if !EnsureServer() {
            return ["error", ""]
        }
        try {
            content := HttpPost("polish", text)
        } catch {
            return ["error", ""]
        }
    }

    content := Trim(content, "`r`n")
    if SubStr(content, 1, 2) = "__" {
        if InStr(content, "NO_KEY")
            return ["no_key", ""]
        return ["error", ""]
    }
    return ["ok", content]
}

; ---------------- 注册热键 ----------------
hotkeyErr := ""
try {
    Hotkey(PolishKey, PolishText)
} catch {
    hotkeyErr := PolishKey
}
if hotkeyErr != "" {
    MsgBox("热键 " hotkeyErr " 已被其他程序占用。请用记事本打开 config\app.ini，修改 [hotkey] 段的 polish_key 为其他按键，保存后重新双击「mosq-ai-fix.exe」。`n`n格式示例：F9、^F9(Ctrl+F9)、!F9(Alt+F9)、+F9(Shift+F9)", "语句润色", "Iconi")
    ExitApp()
}

if IsAIEnabled()
    TrayTip("语句润色已运行（AI 已开启）", "任意输入框按 " PolishKey " 润色语句", 3)
else
    TrayTip("语句润色已运行", "AI 未启用：编辑 config\app.ini 填入 API Key 后重启", 5)

; ---------------- 主流程：语句润色（v5.0，默认 F9）----------------
; 读取文本：默认全选整个输入框内容
PolishText(*) {
    global PolishKey
    ; 通用模式：任意可编辑输入框都能润色（微信/浏览器/记事本/编辑器等）
    if !IsEditableFocused() {
        MsgBox("当前焦点不在文本输入框里，请先点击要润色的输入框（微信聊天框、网页文本框、记事本等均可），再按 " PolishKey, "语句润色", "Iconi")
        return
    }

    ; 1. 保存剪贴板，读取输入框全部内容（默认全选）
    saved := ClipboardAll()
    A_Clipboard := ""
    Send("^a")
    Sleep(80)
    Send("^c")
    if !ClipWait(0.8) {
        A_Clipboard := saved
        ShowAutoCloseTip("没读到文字", "请先点击输入框，再按 " . PolishKey, 1000)
        return
    }
    text := A_Clipboard
    A_Clipboard := saved

    if Trim(text) = "" {
        ShowAutoCloseTip("没有可润色的文字", "先在输入框里输入内容，再按 " . PolishKey, 1000)
        return
    }

    ; 2. 检查 AI 配置
    if !IsAIEnabled() {
        MsgBox("AI 校对未启用。请用记事本打开 config\app.ini，填入云端 API Key 并设 enabled=true", "语句润色", "Iconi")
        return
    }

    ; 3. 调用云端 AI 润色（约 1-5 秒，鼠标旁会显示"润色中"提示）
    ToolTip("正在润色，请稍候…")
    p := RunPolish(text)
    ToolTip()

    if p[1] = "no_key" {
        MsgBox("未检测到 API Key。请用记事本打开 config\app.ini 填入", "语句润色", "Iconi")
        return
    }
    if p[1] = "no_python" {
        MsgBox("未能启动校对程序 check_ai.exe。请确认工具目录里有 check_ai.exe（Go 编译，无需安装 Python），且未被杀毒软件拦截；仍不行请重新解压/复制整个工具目录。", "语句润色", "Iconi")
        return
    }
    if p[1] = "error" {
        MsgBox("AI 调用失败，请检查网络连接后重试；如超时可把 config\app.ini 里的 timeout 调大（当前 15 秒）", "语句润色", "Iconi")
        return
    }

    ; 4. 弹窗预览润色结果（可编辑微调），确认后替换
    ShowPolishGui(text, p[2], WinGetID("A"))
}

; ---------------- 润色结果弹窗（v5.0）----------------
; 展示润色后的文本，Edit 可直接编辑微调；点"替换原文"回填输入框
ShowPolishGui(orig, polished, targetHwnd) {
    myGui := Gui("+AlwaysOnTop", "语句润色 · 预览")
    myGui.SetFont("s10", "Microsoft YaHei")
    myGui.Add("Text", "w560", "润色结果（可直接编辑微调，点「替换原文」回填输入框）：")
    edit := myGui.Add("Edit", "w560 h200 WantTab", polished)

    btnApply := myGui.Add("Button", "w130 h32 Default", "替换原文")
    btnClose := myGui.Add("Button", "x+12 w100 h32", "取消")
    btnApply.OnEvent("Click", (*) => ApplyPolish(myGui, edit, targetHwnd))
    btnClose.OnEvent("Click", (*) => myGui.Destroy())
    myGui.Show()
}

; ---------------- 润色替换回填（v5.0）----------------
ApplyPolish(myGui, editCtrl, targetHwnd) {
    newText := editCtrl.Text
    if Trim(newText) = "" {
        MsgBox("润色结果为空，无法替换。请在弹窗里手动编辑内容后再点「替换原文」，或点「取消」。", "语句润色", "Iconi")
        return
    }

    saved := ClipboardAll()
    A_Clipboard := newText
    myGui.Hide()               ; 先隐藏弹窗，露出原窗口
    WinActivate(targetHwnd)    ; 把焦点还给原输入框（关键修复）
    Sleep(150)                 ; 等窗口激活完成
    Send("^a")
    Sleep(80)
    Send("^v")
    Sleep(120)
    A_Clipboard := saved
    myGui.Destroy()
    ; 右下角提醒可按配置关闭（[ui] tray_tip=false 时不弹）
    if TrayTipEnabled()
        TrayTip("润色完成", "已替换为润色后的文本", 3)
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
    tipGui := Gui("+AlwaysOnTop -Caption +ToolWindow +E0x08000000", "语句润色")
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
