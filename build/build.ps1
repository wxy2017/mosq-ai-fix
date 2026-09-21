#Requires -Version 5.1
<#
  build.ps1 — 一键构建「文本润色工具（mosq-ai-fix）」，仅 Windows

  流程：windres 生成图标资源 → 编译后端 → 编译启动器 → 同步运行时依赖与配置模板到 dist\

  用法（仓库根下）：
    powershell -ExecutionPolicy Bypass -File build\build.ps1

  产出：dist\ 即为可分发的部署区（压包即可交付）
  日志：同时写入 build\build.log（便于在无控制台环境下排查）

  注意：若本工具正在运行，其后端 check_ai.exe 会锁定输出文件。
        —— 该进程由前端自提权启动，普通权限的终端可能无权限结束它；
           此时请手动退出工具（托盘图标 → Exit）后重新构建。
#>
param(
    [switch]$KeepRunning   # 构建前不尝试结束 check_ai.exe
)

$buildDir = $PSScriptRoot
$logFile = Join-Path $buildDir 'build.log'
try { Start-Transcript -Path $logFile -Force -ErrorAction Stop | Out-Null } catch { }

$ErrorActionPreference = 'Stop'
$failed = $false

function Step { param($m) Write-Host "==> $m" -ForegroundColor Cyan }
function Warn { param($m) Write-Host "!!  $m" -ForegroundColor Yellow }

try {
    if (-not $RepoRoot) { $RepoRoot = Split-Path -Parent $buildDir }
    . (Join-Path $buildDir 'dev.env.ps1')

    $dist        = Join-Path $RepoRoot 'dist'
    $distRt      = Join-Path $dist 'runtime'
    $distCfg     = Join-Path $dist 'config'
    $srcDir      = Join-Path $RepoRoot 'src'
    $srcLauncher = Join-Path $srcDir 'launcher'
    $srcFrontend = Join-Path $srcDir 'frontend\typo_check.ahk'

    Write-Host "构建 mosq-ai-fix v$Version  (仓库根: $RepoRoot)" -ForegroundColor Green

    # ---- 0) 前置检查 ----
    Step '检查工具链'
    if (-not (Test-Path $GoExe)) { throw "找不到 Go: $GoExe（请改 build\dev.env.ps1）" }
    if (-not (Test-Path $WindresExe)) { Warn "找不到 windres: $WindresExe —— 编译出的 exe 将没有图标" }
    if (-not (Test-Path $AhkExe)) { throw "找不到 AutoHotkey 运行时: $AhkExe" }

    Step '检查是否占用输出文件'
    $running = @(Get-Process -Name 'check_ai' -ErrorAction SilentlyContinue)
    if ($running.Count -gt 0) {
        if ($KeepRunning) {
            Warn "check_ai.exe 正在运行（$($running.Count) 个），按 -KeepRunning 跳过结束步骤；若编译报文件被占用，请先退出工具"
        } else {
            try {
                $running | Stop-Process -Force -ErrorAction Stop
                Start-Sleep -Milliseconds 500
                Write-Host "   已结束 $($running.Count) 个 check_ai.exe"
            } catch {
                Warn "无法结束 check_ai.exe（该进程以管理员权限运行，当前终端权限不足）"
                Warn "→ 请从托盘退出本工具后重新构建；本次将继续尝试编译"
            }
        }
    } else {
        Write-Host '   无运行中的 check_ai.exe'
    }

    foreach ($d in @($distRt, $distCfg)) {
        if (-not (Test-Path $d)) { New-Item -ItemType Directory -Path $d -Force | Out-Null }
    }

    # ---- 1) 图标资源：app.rc -> app.syso ----
    # app.syso 必须与 main.go 同目录，Go 才会自动嵌入；属构建产物，已 gitignore
    Step '生成图标资源 app.syso'
    if (Test-Path $WindresExe) {
        & $WindresExe -i (Join-Path $srcLauncher 'app.rc') -o (Join-Path $srcLauncher 'app.syso')
        if ($LASTEXITCODE -ne 0) {
            throw 'windres 失败——检查 app.rc 内路径是否用正斜杠（应为 ../assets/icon.ico，反斜杠会被当转义符）'
        }
        Write-Host "   app.syso $((Get-Item (Join-Path $srcLauncher 'app.syso')).Length) 字节"
    }

    # ---- 2) 后端 check_ai.exe ----
    Step '编译后端 check_ai.exe'
    Push-Location $srcDir
    try {
        & $GoExe build -o (Join-Path $distRt 'check_ai.exe') ./backend
        if ($LASTEXITCODE -ne 0) { throw '后端编译失败（若提示文件被占用，请先退出正在运行的工具）' }
    } finally { Pop-Location }

    # ---- 3) 启动器 mosq-ai-fix.exe ----
    Step '编译启动器 mosq-ai-fix.exe'
    $ldflags = '-H windowsgui'
    if ($StripSymbols) { $ldflags = "$ldflags -s -w" }
    Push-Location $srcLauncher
    try {
        & $GoExe build -ldflags $ldflags -o (Join-Path $dist 'mosq-ai-fix.exe') .
        if ($LASTEXITCODE -ne 0) { throw '启动器编译失败' }
    } finally { Pop-Location }

    # ---- 4) 同步运行时依赖 ----
    Step '同步运行时依赖到 dist\runtime'
    Copy-Item $AhkExe                                        $distRt -Force
    Copy-Item (Join-Path $RepoRoot 'tools\ahk\license.txt')  $distRt -Force
    Copy-Item (Join-Path $srcDir 'assets\icon.ico')          $distRt -Force
    try {
        Copy-Item $srcFrontend $distRt -Force
    } catch {
        throw "复制 typo_check.ahk 失败：请退出正在运行的工具（AutoHotkey64.exe）后重试"
    }

    # ---- 5) 同步配置模板（绝不覆盖已有 app.ini）----
    Step '同步配置模板'
    Copy-Item (Join-Path $buildDir 'templates\app.example.ini') $distCfg -Force
    $appIni = Join-Path $distCfg 'app.ini'
    if (Test-Path $appIni) {
        Write-Host '   保留现有 dist\config\app.ini（未覆盖）'
    } else {
        Copy-Item (Join-Path $distCfg 'app.example.ini') $appIni
        Warn '已生成 dist\config\app.ini —— 请填入 api_key'
    }

    # ---- 6) 汇总 ----
    Step '构建完成，dist\ 内容如下'
    $total = (Get-ChildItem $dist -Recurse -File | Measure-Object Length -Sum).Sum
    Write-Host ("   部署区合计 {0:N1} MB" -f ($total / 1MB))
    Get-ChildItem $dist -Recurse -File | Sort-Object FullName | ForEach-Object {
        Write-Host ('   {0,9:N0} KB  {1}' -f ($_.Length / 1KB), $_.FullName.Replace("$RepoRoot\", ''))
    }
    # v5.3 起已移除磁盘缓存，构建不再生成 dist\.cache\；此处仅提示历史遗留目录
    if (Test-Path (Join-Path $dist '.cache')) {
        Warn '发现旧版遗留的 dist\.cache\（v5.3 起已不再生成），可安全删除'
    }

    # ---- 7) 前端自检 ----
    if ($RunSelfTest) {
        Step '前端自检（-selftest，跑完即退，不注册热键）'
        # 自检报告由 AHK 写入 %TEMP%\mosq_selftest.txt（权威来源）。
        # 不能依赖 stdout：GUI 子系统在 PowerShell 的 & 调用下拿不到可用的 stdout 句柄，
        # 只读 stdout 会得到空结果，自检看起来"跑了"，实际无法判定。
        $selftestReport = Join-Path $env:TEMP 'mosq_selftest.txt'
        Remove-Item $selftestReport -Force -ErrorAction SilentlyContinue
        # 注意：绝不可写成 `& $AhkExe ... | Out-Null` 之类的管道/重定向 ——
        # 自检会拉起常驻 check_ai.exe，它继承 stdout 句柄且长期不退出，
        # 管道因此永不结束，构建会假死在自检这一步。
        & $AhkExe (Join-Path $distRt 'typo_check.ahk') -selftest
        if (Test-Path $selftestReport) {
            $lines = @(Get-Content $selftestReport -Encoding UTF8)
            foreach ($l in $lines) { Write-Host "   $l" }
            # 网络/密钥问题不应让构建失败，但必须显式提示
            $status = ($lines | Where-Object { $_ -like '润色状态:*' } | Select-Object -First 1)
            if ($status -and $status -notmatch '润色状态:\s*ok') {
                Warn "自检未通过：$status（不影响产物生成，请检查网络与 config\app.ini）"
            }
        } else {
            Warn '自检未生成报告 —— 自检可能未真正执行，请手动确认'
        }
    }

    Write-Host 'BUILD OK' -ForegroundColor Green
} catch {
    $failed = $true
    Write-Host 'BUILD FAILED' -ForegroundColor Red
    Write-Host "  原因: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "  位置: $($_.InvocationInfo.PositionMessage)" -ForegroundColor DarkGray
} finally {
    try { Stop-Transcript | Out-Null } catch { }
}

if ($failed) { exit 1 }
exit 0
