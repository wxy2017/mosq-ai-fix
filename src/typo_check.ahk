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
; 文本校对工具（错字检查 + 语句润色）  v5.0（Go 版 · 免 Python 环境）
; 用法：在任意可编辑的输入框（微信聊天框、网页文本框、
;       记事本、代码编辑器等）打好字后：
;         按 F8 检查错别字/用词不当，发现错误会弹窗列出，
;             点"一键修正"自动替换回输入框
;         按 F9 润色当前语句，改写得更得体、通顺、易理解，
;             弹窗预览（可编辑微调），点"替换原文"回填
;       两个热键都可在 typo_config.ini 的 [hotkey] 段修改
; 说明：本工具不修改任何程序，仅模拟复制/粘贴，安全无风险
;
; v4.0 变更（由 Python 版重构为 Go 版）：
;  1. 校对中间层由 check_ai.py 重写为 check_ai.exe（Go 编译，
;     单文件、免安装、无 Python 环境依赖），接口完全兼容
;  2. 不再需要 python_cmd 配置，直接运行同目录 check_ai.exe
;  3. 其余功能不变：任意可编辑输入框按 F8、焦点可编辑检测、
;     严格模式 AI 校对（智谱 GLM-4-Flash）、一键修正回填、
;     调试日志 ai_debug.log
; v4.2 变更：
;  1. 新增编译版「错字检查.exe」（Ahk2Exe 编译，双击即用，
;     不再需要 AutoHotkey 运行时和 bat 启动器）
;  2. 基准目录 BaseDir 兼容源码/编译两种运行方式
; v4.3 变更：
;  1. 校对能力升级：除错别字外，新增"明显的用词不当/语句不通顺"
;     检查（如"出了以上方法"应为"除了以上方法"）
; v5.0 变更（第二版本）：
;  1. 新增语句润色功能：按 F9（可配置 [hotkey] polish_key）
;     改写当前语句，使其更得体、通顺、易理解
;  2. 润色默认处理整个输入框内容（与错字检查一致，全选），
;     不再优先读取选中文本
;  3. 润色结果弹窗预览，支持手动微调后"替换原文"
;  注意：检查/润色文本会发送到智谱云端，请勿输入敏感内容
; =============================================================

; 目录结构（v4.2）：基准目录 BaseDir = 工具根
;   源码版（脚本在 src\ 下）：src 的上一级；编译版（exe 在工具根）：就是 exe 所在目录
;   配置在 config\，校对程序在 src\bin\
global BaseDir := A_IsCompiled ? A_ScriptDir : A_ScriptDir "\.."
global CfgIni := BaseDir "\config\typo_config.ini"  ; 配置文件
global TriggerKey := LoadHotkey()      ; [hotkey] key 读取，失败回退 F8（错字检查）
global PolishKey := LoadPolishHotkey() ; [hotkey] polish_key 读取，失败回退 F9（语句润色）

; 源码版运行时用项目图标作托盘图标（编译版自动使用嵌入图标）
if !A_IsCompiled {
    icoFile := A_ScriptDir "\assets\icon.ico"
    if FileExist(icoFile)
        TraySetIcon(icoFile)
}

; ---------------- 自检模式：错字检查.exe -selftest（源码版：AutoHotkey64.exe typo_check.ahk -selftest）----------------
if A_Args.Length > 0 && A_Args[1] = "-selftest" {
    RunSelfTest()
    ExitApp()
}

RunSelfTest() {
    FileAppend("AI 校对: " (IsAIEnabled() ? "已启用" : "未启用（编辑 typo_config.ini 配置 API Key 后启用）") "`n", "*")
    FileAppend("检查热键: " TriggerKey "（改 typo_config.ini 的 [hotkey] key 后重启生效）`n", "*")
    FileAppend("润色热键: " PolishKey "（改 typo_config.ini 的 [hotkey] polish_key 后重启生效）`n", "*")
    FileAppend("右下角提醒: " (TrayTipEnabled() ? "开启（[ui] tray_tip=true）" : "关闭（[ui] tray_tip=false）") "`n", "*")
    sample := "今天的会议按步就班进行，希望大家再接再励。既使遇到问题也要冷静面对。他亨受生活，工作也很努立。出了以上方法还有其他方法吗。"
    ai := RunAICheck(sample)
    FileAppend("检查状态: " ai[1] "，结果 " ai[2].Length " 条`n", "*")
    for r in ai[2]
        FileAppend("  " r[1] " -> " r[2] " | " r[3] "`n", "*")
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
; 从 typo_config.ini 的 [hotkey] key 读取（如 F8、^F8、!F8、F9），读取失败/为空回退 F8
LoadHotkey() {
    global CfgIni
    try {
        k := Trim(IniRead(CfgIni, "hotkey", "key"))
        if k != ""
            return k
    }
    return "F8"
}

; 从 typo_config.ini 的 [hotkey] polish_key 读取润色热键（v5.0），失败回退 F9
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
; 从 typo_config.ini 的 [ui] tray_tip 读取：true=一键修正后在右下角弹
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

; ---------------- 调用 check_ai.exe（Go 校对中间层）----------------
; 返回 [status, results]
;   status : "ok" 正常 | "no_key" 未配置 Key | "no_python" 未能启动程序 | "error" 调用失败
;   results: [[错误, 正确, 原因], ...]
RunAICheck(text) {
    global BaseDir
    exePath := BaseDir "\src\bin\check_ai.exe"
    inFile := BaseDir "\src\tmp_ai_in.txt"
    outFile := BaseDir "\src\tmp_ai_out.txt"
    FileAppend(text, inFile, "UTF-8")

    ran := false
    try {
        if FileExist(outFile)
            FileDelete(outFile)
        RunWait('"' exePath '" "' inFile '" "' outFile '"', BaseDir, "Hide")
        ; 输出文件生成 = 校对程序确实执行了
        if FileExist(outFile)
            ran := true
    } catch {
        ; exe 不存在或被占用
        ran := false
    }

    if !ran {
        if FileExist(inFile)
            FileDelete(inFile)
        return ["no_python", []]
    }

    results := []
    status := "ok"
    if FileExist(outFile) {
        content := FileRead(outFile, "UTF-8-RAW")
        for line in StrSplit(content, "`n", "`r") {
            line := Trim(line)
            if line = ""
                continue
            if SubStr(line, 1, 2) = "__" {
                if InStr(line, "NO_KEY")
                    status := "no_key"
                else if InStr(line, "ERROR")
                    status := "error"
                ; __NONE__ 视为 ok（无错误）
                continue
            }
            parts := StrSplit(line, "`t")
            if parts.Length >= 3 && Trim(parts[1]) != ""
                results.Push([Trim(parts[1]), Trim(parts[2]), Trim(parts[3])])
        }
        FileDelete(outFile)
    }
    if FileExist(inFile)
        FileDelete(inFile)
    return [status, results]
}

; ---------------- 调用 check_ai.exe 润色模式（v5.0）----------------
; 返回 [status, polishedText]
;   status : "ok" 正常 | "no_key" 未配置 Key | "no_python" 未能启动程序 | "error" 调用失败
;   text   : 润色后的整段文本（ok 时）
RunPolish(text) {
    global BaseDir
    exePath := BaseDir "\src\bin\check_ai.exe"
    inFile := BaseDir "\src\tmp_polish_in.txt"
    outFile := BaseDir "\src\tmp_polish_out.txt"
    FileAppend(text, inFile, "UTF-8")

    ran := false
    try {
        if FileExist(outFile)
            FileDelete(outFile)
        RunWait('"' exePath '" -polish "' inFile '" "' outFile '"', BaseDir, "Hide")
        ; 输出文件生成 = 校对程序确实执行了
        if FileExist(outFile)
            ran := true
    } catch {
        ran := false
    }

    if !ran {
        if FileExist(inFile)
            FileDelete(inFile)
        return ["no_python", ""]
    }

    content := ""
    if FileExist(outFile) {
        content := FileRead(outFile, "UTF-8-RAW")
        FileDelete(outFile)
    }
    if FileExist(inFile)
        FileDelete(inFile)
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
    Hotkey(TriggerKey, CheckAndFix)
} catch {
    hotkeyErr := TriggerKey
}
try {
    Hotkey(PolishKey, PolishText)
} catch {
    if hotkeyErr = ""
        hotkeyErr := PolishKey
    else
        hotkeyErr .= "、" PolishKey
}
if hotkeyErr != "" {
    MsgBox("热键 " hotkeyErr " 已被其他程序占用。请用记事本打开 typo_config.ini，修改 [hotkey] 段的 key 或 polish_key 为其他按键，保存后重新双击「mosq-ai-fix.exe」。`n`n格式示例：F8、F9、^F8(Ctrl+F8)、!F8(Alt+F8)、+F8(Shift+F8)", "错字检查", "Iconi")
    ExitApp()
}

if IsAIEnabled()
    TrayTip("错字检查已运行（AI 已开启）", "任意输入框按 " TriggerKey " 检查错字，按 " PolishKey " 润色语句", 3)
else
    TrayTip("错字检查已运行", "AI 未启用：编辑 typo_config.ini 填入 API Key 后重启（见使用说明.txt）", 5)

; ---------------- 主流程 ----------------
CheckAndFix(*) {
    global TriggerKey
    ; 通用模式：任意可编辑输入框都能检查（微信/浏览器/记事本/编辑器等）
    if !IsEditableFocused() {
        MsgBox("当前焦点不在文本输入框里，请先点击要检查的输入框（微信聊天框、网页文本框、记事本等均可），再按 " TriggerKey, "错字检查", "Iconi")
        return
    }

    ; 1. 保存剪贴板，读取输入框内容
    saved := ClipboardAll()
    A_Clipboard := ""
    Send("^a")
    Sleep(80)
    Send("^c")
    if !ClipWait(0.8) {
        A_Clipboard := saved
        ShowAutoCloseTip("没读到文字", "请先点击输入框，再按 " . TriggerKey, 1000, "info")
        return
    }
    text := A_Clipboard
    A_Clipboard := saved

    if Trim(text) = "" {
        ShowAutoCloseTip("输入框里没有文字", "先在输入框里输入内容，再按 " . TriggerKey, 1000, "info")
        return
    }

    ; 2. 检查 AI 配置
    if !IsAIEnabled() {
        MsgBox("AI 校对未启用。请用记事本打开 typo_config.ini，填入智谱 API Key 并设 enabled=true（免费，获取步骤见使用说明.txt）", "错字检查", "Iconi")
        return
    }

    ; 3. 调用云端 AI 校对（约 1-5 秒，鼠标旁会显示"校对中"提示）
    ToolTip("正在调用 AI 校对，请稍候…")
    ai := RunAICheck(text)
    ToolTip()

    if ai[1] = "no_key" {
        MsgBox("未检测到 API Key。请用记事本打开 typo_config.ini 填入（免费获取步骤见使用说明.txt）", "错字检查", "Iconi")
        return
    }
    if ai[1] = "no_python" {
        MsgBox("未能启动校对程序 check_ai.exe。请确认工具目录里有 check_ai.exe（Go 编译，无需安装 Python），且未被杀毒软件拦截；仍不行请重新解压/复制整个工具目录。", "错字检查", "Iconi")
        return
    }
    if ai[1] = "error" {
        MsgBox("AI 调用失败，请检查网络连接后重试；如超时可把 typo_config.ini 里的 timeout 调大（当前 15 秒）", "错字检查", "Iconi")
        return
    }
    if ai[2].Length = 0 {
        ; 无错误提示：白卡片淡入显示，2 秒后自动淡出消失，不打断操作
        ShowAutoCloseTip()
        return
    }

    ; 4. 弹窗展示结果（记住原窗口句柄：点"一键修正"时要把焦点还给原输入框才能粘贴回去）
    ShowResultGui(text, ai[2], WinGetID("A"))
}

; ---------------- 语句润色主流程（v5.0，默认 F9）----------------
; 读取文本：与错字检查一致，默认全选整个输入框内容
PolishText(*) {
    global PolishKey
    ; 通用模式：任意可编辑输入框都能润色（微信/浏览器/记事本/编辑器等）
    if !IsEditableFocused() {
        MsgBox("当前焦点不在文本输入框里，请先点击要润色的输入框（微信聊天框、网页文本框、记事本等均可），再按 " PolishKey, "语句润色", "Iconi")
        return
    }

    ; 1. 保存剪贴板，读取输入框全部内容（与错字检查一致：默认全选）
    saved := ClipboardAll()
    A_Clipboard := ""
    Send("^a")
    Sleep(80)
    Send("^c")
    if !ClipWait(0.8) {
        A_Clipboard := saved
        ShowAutoCloseTip("没读到文字", "请先点击输入框，再按 " . PolishKey, 1000, "info")
        return
    }
    text := A_Clipboard
    A_Clipboard := saved

    if Trim(text) = "" {
        ShowAutoCloseTip("没有可润色的文字", "先在输入框里输入内容，再按 " . PolishKey, 1000, "info")
        return
    }

    ; 2. 检查 AI 配置
    if !IsAIEnabled() {
        MsgBox("AI 校对未启用。请用记事本打开 typo_config.ini，填入智谱 API Key 并设 enabled=true（免费，获取步骤见使用说明.txt）", "语句润色", "Iconi")
        return
    }

    ; 3. 调用云端 AI 润色（约 1-5 秒，鼠标旁会显示"润色中"提示）
    ToolTip("正在润色，请稍候…")
    p := RunPolish(text)
    ToolTip()

    if p[1] = "no_key" {
        MsgBox("未检测到 API Key。请用记事本打开 typo_config.ini 填入（免费获取步骤见使用说明.txt）", "语句润色", "Iconi")
        return
    }
    if p[1] = "no_python" {
        MsgBox("未能启动校对程序 check_ai.exe。请确认工具目录里有 check_ai.exe（Go 编译，无需安装 Python），且未被杀毒软件拦截；仍不行请重新解压/复制整个工具目录。", "语句润色", "Iconi")
        return
    }
    if p[1] = "error" {
        MsgBox("AI 调用失败，请检查网络连接后重试；如超时可把 typo_config.ini 里的 timeout 调大（当前 15 秒）", "语句润色", "Iconi")
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
; 未发现错误 / 输入框无文字 等轻量场景的提示：白色卡片 + 左侧主题色条 + 图标 +
; 主副标题两行，Win11 下自动圆角；淡入显示、durationMs 后淡出销毁；
; 不抢焦点、无需用户点击。有错误时仍走 ShowResultGui 列表弹窗
; kind 参数：ok=绿色成功(✓)   info=蓝色提示(ℹ，如"输入框里没有文字")
; 注意：必须用全局引用 TipGuiRef 持有 Gui 对象——AHK v2 中若引用计数归零，
;       窗口会被自动销毁，导致淡出定时器访问 Hwnd 时抛 "Gui has no window"
ShowAutoCloseTip(title := "未发现错字或用词问题", subtitle := "AI 校对通过，可以放心使用", durationMs := 1000, kind := "ok") {
    global TipGuiRef
    ; 主题色与图标
    if kind = "info" {
        barColor := "3B82F6"          ; 蓝色主题条
        iconColor := "3B82F6"
        iconChar := "ℹ"
    } else {
        barColor := "22C55E"          ; 绿色主题条（成功）
        iconColor := "22C55E"
        iconChar := "✓"
    }
    tipGui := Gui("+AlwaysOnTop -Caption +ToolWindow +E0x08000000", "错字检查")
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

; ---------------- 结果弹窗 ----------------
ShowResultGui(orig, found, targetHwnd) {
    myGui := Gui("+AlwaysOnTop", "错字检查 · 发现 " found.Length " 处疑似错误")
    myGui.SetFont("s10", "Microsoft YaHei")
    myGui.Add("Text", "w520", "以下写法疑似有误，点「一键修正」会自动替换回原输入框：")
    lv := myGui.Add("ListView", "w520 h240", ["错误写法", "建议修正", "原因"])
    for pair in found {
        lv.Add("", pair[1], pair[2], pair[3])
    }
    lv.ModifyCol(1, 150)
    lv.ModifyCol(2, 150)
    lv.ModifyCol(3, 170)

    btnFix := myGui.Add("Button", "w130 h32", "一键修正")
    btnClose := myGui.Add("Button", "x+12 w100 h32 Default", "关闭")
    btnFix.OnEvent("Click", (*) => ApplyFix(myGui, orig, found, targetHwnd))
    btnClose.OnEvent("Click", (*) => myGui.Destroy())
    myGui.Show()
}

; ---------------- 一键修正并回填 ----------------
ApplyFix(myGui, orig, found, targetHwnd) {
    newText := orig
    for pair in found {
        newText := StrReplace(newText, pair[1], pair[2])
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
        TrayTip("已修正并回填", "确认无误后即可发送", 3)
}
