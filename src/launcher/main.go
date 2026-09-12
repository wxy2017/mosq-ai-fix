// launcher — mosq-ai-fix.exe 启动器（GUI 子系统，无控制台窗口）
//
// 职责：双击后拉起真正的校对前端 src/typo_check.ahk（AutoHotkey v2），
// 由它提供托盘图标、F8/F9 热键，并调用同目录的 check_ai.exe（Go 校对后端）。
// 本程序本身只负责启动，启动后即退出，不驻留。
//
// 编译（在 src/launcher 目录）：
//   go build -ldflags "-H windowsgui" -o ../../mosq-ai-fix.exe .
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
	rootDir := filepath.Dir(exePath) // mosq-ai-fix.exe 所在目录（工具根）
	ahkExe := filepath.Join(rootDir, "src", "lib", "ahk", "AutoHotkey64.exe")
	script := filepath.Join(rootDir, "src", "typo_check.ahk")

	// 应用开机自启动配置（仅 Windows；由 config\typo_config.ini [startup] auto_start 控制，默认关闭）。
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
