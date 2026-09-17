# dev.env.ps1 — 开发/构建环境配置（仅开发机使用，不随 dist 分发）
#
# 由 build.ps1 点源加载：  . .\dev.env.ps1
# 这些参数只影响「怎么编译」，不影响「怎么运行」——运行期参数一律在 dist\config\app.ini。
#
# 如需在本机改变工具链位置，只改本文件即可，不必动源码或 README。

# 仓库根（本文件位于 build\ 下，上级即仓库根）
if (-not $RepoRoot) { $RepoRoot = Split-Path -Parent $PSScriptRoot }

# ---- 工具链路径 ----
# Go 1.23+（编译后端与启动器，两者均纯标准库）
$GoExe = 'C:\Program Files\Go\bin\go.exe'

# windres（MinGW/w64devkit）——把 src\launcher\app.rc 编译成 app.syso 以嵌入图标。
# 缺失会导致编译出的 mosq-ai-fix.exe 没有图标（编译仍会成功，只是丢图标）。
$WindresExe = 'D:\evn\w64devkit-1.22.0\w64devkit\bin\windres.exe'

# AutoHotkey 运行时（复用 tools 下的厂商原件，构建时复制到 dist\runtime）
$AhkExe = Join-Path $RepoRoot 'tools\ahk\AutoHotkey64.exe'

# ---- 构建选项 ----
# 版本号仅用于构建日志展示；真正的缓存失效标记是
# src\backend\check_ai.go 里的 promptVersion（改提示词时必须同步改它）
$Version = '5.3'

# $true = 加 -ldflags "-s -w" 精简体积（约减 25%）；$false = 保留符号便于调试
$StripSymbols = $false

# 构建完成后是否自动跑前端自检（AutoHotkey64.exe typo_check.ahk -selftest）
$RunSelfTest = $true
