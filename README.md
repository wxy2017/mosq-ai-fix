
 ##  文本润色工具（语句润色）
  
  任意输入框按 F9 把语句润色得更得体、通顺


### 【它是什么】
  在微信、网页、记事本等任意输入框打好字：
  - 按 F9，把当前语句改写得更得体、通顺、易理解（默认润色整个输入框内容），弹窗预览可微调后替换
  - 调用云端大模型完成润色，本地只做复制/粘贴，不修改任何程序

### 【部署：只发 dist\ 目录】
  dist\ 就是完整的可分发单元 —— 压包发给对方即可，对方无需安装任何东西。

```
dist\
├─ mosq-ai-fix.exe        入口，双击运行
├─ config\
│  ├─ app.ini             部署参数（填 api_key、改热键都在这里）
│  └─ app.example.ini     模板（key 留空）
└─ runtime\               运行时依赖，请勿移动或改名
   ├─ typo_check.ahk      前端脚本（F9 热键）
   ├─ AutoHotkey64.exe    AutoHotkey v2 便携解释器
   ├─ check_ai.exe        Go 润色后端
   ├─ icon.ico            托盘图标
   └─ license.txt         AutoHotkey 许可证
```

  > 本工具不生成任何运行时缓存：`dist\` 构建完即为最终交付内容，压包即可发。

  部署端要求：
  - Windows x64（仅此一项）
  - 能访问所配置 API 的网址
  - **不需要**：管理员权限、Python、VC++ 运行库、AutoHotkey 安装包、Go

### 【验证部署是否正常】
  在 dist 目录下用命令行跑一次自检（会读配置并做一次真实润色）：
```
cd dist\runtime
AutoHotkey64.exe typo_check.ahk -selftest
```
  输出示例：
```
AI 校对: 已启用
润色热键: F9
右下角提醒: 关闭（[ui] tray_tip=false）
润色状态: ok，结果长度 21 字
  我今天确实很想去，只是时间上似乎不太充裕。
```
  自检报告同时写入 `%TEMP%\mosq_selftest.txt`（UTF-8），便于脚本化判定。
  若显示「未启用」→ 检查 `dist\config\app.ini` 的 `api_key` 与 `enabled=true`；
  若显示 `no_key` / `error` 前缀 → 表示密钥未配置或网络不可达。

### 【调用日志】
  - 位置：`dist\config\ai_debug.log`，记录每次大模型调用的
    **请求 URL、完整请求参数、响应耗时、HTTP 状态与完整响应体**（不含 api_key）
  - 开关与上限：`app.ini` 的 `[log] enabled` / `[log] max_size_mb`（默认开启、上限 5MB）
  - 超出上限时自动丢弃最旧的记录，文件体积恒定不越界
  - 该文件属运行时数据，**不在交付清单内**：打包发给别人前可删

### 【使用方法】
  1. 双击 dist\mosq-ai-fix.exe
  2. 点进输入框打好字 → 按 F9 润色语句 → 等 1~5 秒
  3. 弹窗显示润色结果 → 可手动编辑微调 → 点「替换原文」回填，或「取消」放弃
     （输入框没文字时，提示约 1 秒自动消失）
  4. 热键可改：用记事本打开 dist\config\app.ini，
     修改 [hotkey] 段的 polish_key（默认 F9）

### 【第一次使用：配置 API Key】
  1. 用记事本打开 dist\config\app.ini
  2. 把 Key 填到 api_key= 后面，确认 enabled=true，保存
  3. 重新双击 dist\mosq-ai-fix.exe
  4. 默认用 DeepSeek；想换厂商只改 model 与 base_url 两行即可（app.ini 内有对照注释）

### 【注意事项】
  - 请配置 API Key 后再使用，否则无法润色
  - 每次润色都会真实调用云端 API（无本地缓存）：重复润色同一句同样会等待 1~5 秒并消耗额度
  - 因此断网时无法润色（即使这句话之前润色过）
  - 同一句话多次润色的结果措辞可能略有不同（模型有随机性，属正常现象）
  - 本地开启 VPN 时，注意所配置的 API 域名能否访问
  - 未使用管理员权限运行时，部分输入框可能无法抓取文字
  - 开机自启动：把 app.ini 的 [startup] auto_start 改为 true，再双击一次主程序即生效

### 【开发：源码与构建】
  源码区与部署区严格分离，源码目录内不含任何产物。

```
src\          源码（不含产物）
├─ go.mod                    module typocheck
├─ backend\check_ai.go       Go 润色后端
├─ frontend\typo_check.ahk   AHK 前端（唯一源，构建时复制到 dist\runtime）
├─ launcher\                 Go 启动器（main.go + autostart*.go + app.rc）
└─ assets\icon.ico           图标唯一源

build\        构建脚本与开发配置（仅开发机使用）
├─ build.ps1                 一键构建：windres → go build ×2 → 同步 dist\
├─ dev.env.ps1               开发配置：Go/windres 路径、版本号、是否 strip
└─ templates\
   ├─ app.example.ini        部署参数模板源（无密钥，随 dist 分发）
   └─ presets\               各厂商个人凭据副本（含真实 key，已 gitignore）

tools\        第三方原件（仅开发用，不随 dist 分发）
├─ ahk\                      AutoHotkey64.exe + license.txt + UX\
└─ compiler\Ahk2Exe.exe      可选：把 .ahk 编成单 exe

dist\         构建产物 = 部署区（已 gitignore，可随时重新生成）
```

  **构建**（在仓库根执行）：
```
powershell -ExecutionPolicy Bypass -File build\build.ps1
```
  - 改「怎么编译」→ 改 `build\dev.env.ps1`
  - 改「怎么运行」→ 改 `dist\config\app.ini`
  - 提示：构建前请退出正在运行的工具，否则输出文件被占用
