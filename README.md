
 ##  文本润色工具（语句润色）
  
  任意输入框按 F9 把语句润色得更得体、通顺


### 【它是什么】
  在微信、网页、记事本等任意输入框打好字：
  - **选中一段文字**后按 F9 → 只润色这一段，**选区之外的文字保持完全不变**
  - **不选中**直接按 F9 → 润色整个输入框内容并整体替换
  - 两种情况都是弹窗预览、可微调后替换
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

### 【程序显示名】
  程序在系统各处显示的名字统一为 **`mosq-ai-fix`**，由两处控制，**改名字要一起改**：

  | 显示位置 | 来源 | 当前值 |
  |---|---|---|
  | 托盘悬停提示、弹窗标题、预览窗标题、通知标题 | `dist\runtime\typo_check.ahk` 顶部的 `AppName` 常量 | `mosq-ai-fix` |
  | 任务管理器、Windows「设置 → 应用 → 启动」、exe 文件属性 | 启动器 exe 内嵌的版本资源：`src\launcher\app.rc` 的 `FileDescription` / `ProductName`（构建时由 windres 写入） | `mosq-ai-fix` |

  > 之所以与文件名取同一个值，是为了让**托盘、界面、任务管理器、exe 文件名**
  > 呈现同一个名字，用户看到的"程序"只有一个名字，不会出现两套叫法。
  > 自检会打印当前生效的显示名（`程序显示名: mosq-ai-fix`），可用来核对。

### 【验证部署是否正常】
  在 dist 目录下用命令行跑一次自检（会读配置并做一次真实润色）：
```
cd dist\runtime
AutoHotkey64.exe typo_check.ahk -selftest
```
  输出示例：
```
程序显示名: mosq-ai-fix
AI 校对: 已启用
润色热键: F9
润色模式: 自动判别选区/全选（[ui] select_mode=auto）
右下角提醒: 关闭（[ui] tray_tip=false）
润色状态: ok，结果长度 21 字
  我今天确实很想去，只是时间上似乎不太充裕。
```
  自检报告同时写入 `%TEMP%\mosq_selftest.txt`（UTF-8），便于脚本化判定。
  若显示「未启用」→ 检查 `dist\config\app.ini` 的 `api_key` 与 `enabled=true`；
  若显示 `no_key` / `error` 前缀 → 表示密钥未配置或网络不可达。

### 【改配置后如何生效（重要）】
  **程序只读取一个文件：`dist\config\app.ini`。**

  同目录下若还有 `deepSeek\`、`freeapi\`、`example\` 之类子目录（里面各自带一份
  app.ini / app.example.ini），**那些文件不会被程序读取**——它们只是你自己留的
  备份或备选模板。想换厂商，请把要用的那份内容覆盖到 `config\app.ini`：

```
copy /Y dist\config\freeapi\app.ini dist\config\app.ini     :: 切到 freeapi
copy /Y dist\config\deepSeek\app.ini dist\config\app.ini    :: 切回 deepseek
```

  - **改完不必重启程序**：后端在每次调用前都会检查 `app.ini` 是否变化，
    一变就立即用新配置（接口地址、模型、Key、超时都会跟着刷新），并在日志里记
    `检测到 app.ini 变更，已热重载配置`。
  - 重启工具同样有效：退出时会自动结束后端进程，下次启动一定用全新进程 + 最新配置。
  - 排查"配置到底生效了没有"，最直接的办法是看 `dist\config\ai_debug.log` 里
    最近一条 `请求URL:` —— 那才是**进程实际请求**的地址。

### 【调用日志】
  - 位置：`dist\config\ai_debug.log`，记录每次大模型调用的
    **请求 URL、完整请求参数、响应耗时、HTTP 状态与完整响应体**（不含 api_key）
  - 开关与上限：`app.ini` 的 `[log] enabled` / `[log] max_size_mb`（默认开启、上限 5MB）
  - 超出上限时自动丢弃最旧的记录，文件体积恒定不越界
  - 该文件属运行时数据，**不在交付清单内**：打包发给别人前可删

### 【使用方法】
  1. 双击 dist\mosq-ai-fix.exe
  2. 点进输入框 → 选好要润色的范围 → 按 F9 → 等 1~5 秒
  3. 弹窗显示润色结果 → 可手动编辑微调 → 点「替换原文」回填，或「取消」放弃
     （输入框没文字时，提示约 1 秒自动消失）
  4. 热键可改：用记事本打开 dist\config\app.ini，
     修改 [hotkey] 段的 polish_key（默认 F9）

### 【两种润色方式：选段 / 全文】
  按 F9 时工具会自动判断你要润色哪部分，无需任何切换开关：

  | 你的操作 | 模式 | 效果 |
  |---|---|---|
  | 先用鼠标/键盘**选中**一段文字，再按 F9 | 手动选区 | 只润色选中这一段；点「替换原文」只覆盖这一段，**其余内容一字不动** |
  | **不选中**任何文字，直接按 F9 | 自动全选 | 润色整个输入框内容，点「替换原文」整体替换 |

  怎么区分的：按 F9 时工具先发一次「复制」（不先全选）——
  - 复制到了文字 → 说明你当前有选区 → 走**选区模式**
  - 复制不到文字 → 说明没有选区 → 走**全选模式**

  > 复制不会取消你的选区，所以选区模式下回填时只需「粘贴」即可精确覆盖原来那段，
  > 不会碰其他文字。弹窗标题与提示行也会写明当前是哪种模式，可据此确认。

  少数编辑器（如 **VSCode**）在"没有选中任何东西"时按 Ctrl+C 会复制整行，
  可能被误判成选区。若你常用这类编辑器润色全文，把 `app.ini` 改成：
```
[ui]
select_mode=all
```
  `auto` 为默认（自动判别），`all` 表示永远全选整个输入框（即旧版行为）。

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
  - 配置改动即时生效，无需重启；日志里的 `请求URL:` 反映的是进程实际请求的地址
  - 后端 `check_ai.exe` 常驻本地 127.0.0.1（不对外），工具退出时会自动回收它；
    若它因异常被强杀残留，下次启动工具时会先清掉它
  - 选区模式下，若在预览弹窗里停留期间原输入框被其他程序改动，回填仍按原选区覆盖，请留意
  - VSCode 等编辑器"无选区按 Ctrl+C 会复制整行"的行为可能干扰自动判别，见上文 `select_mode=all`
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
