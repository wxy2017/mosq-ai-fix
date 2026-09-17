// launcher — mosq-ai-fix.exe 启动器（GUI 子系统，无控制台窗口）
//
// 部署布局（v5.3）：本程序位于 <部署根>\，运行时依赖全部在 <部署根>\runtime\。
//   <部署根>\mosq-ai-fix.exe   入口（本程序）
//   <部署根>\config\app.ini    部署参数
//   <部署根>\runtime\          typo_check.ahk + AutoHotkey64.exe + check_ai.exe + icon.ico
//
// 职责：双击后拉起 runtime 下的润色前端 typo_check.ahk（AutoHotkey v2），
// 由它提供托盘图标、F9 润色热键，并调用同目录的 check_ai.exe（Go 润色后端）。
// 本程序本身只负责启动，启动后即退出，不驻留。
//
// 编译：推荐用 build\build.ps1（先 windres 再编译），或手动——
//   cd src/launcher && go build -ldflags "-H windowsgui" -o ../../dist/mosq-ai-fix.exe .
// 图标由同目录 app.syso（windres 从 src/assets/icon.ico 生成）自动嵌入。

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func main() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	rootDir := filepath.Dir(exePath) // mosq-ai-fix.exe 所在目录（= 部署根 dist/）
	runtimeDir := filepath.Join(rootDir, "runtime")
	ahkExe := filepath.Join(runtimeDir, "AutoHotkey64.exe")
	script := filepath.Join(runtimeDir, "typo_check.ahk")

	// 应用开机自启动配置（仅 Windows；由 config\app.ini [startup] auto_start 控制，默认关闭）。
	// 放在文件检查之前：自启动注册独立于当前文件是否齐备，更贴近"登录时运行本程序"的语义。
	applyAutoStart(exePath, readAutoStart(rootDir))

	// 文件缺失时静默退出（用户可检查工具目录完整性）
	if _, err := os.Stat(ahkExe); err != nil {
		return
	}
	if _, err := os.Stat(script); err != nil {
		return
	}

	cmd := exec.Command(ahkExe, script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// 启动后即返回，AHK 进程独立驻留提供热键服务
	_ = cmd.Start()
}
