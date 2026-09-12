//go:build !windows

package main

// 非 Windows 平台占位实现：自启动仅支持 Windows（HKCU Run）。
// 用 build tag 隔离，保证交叉编译/非 Windows 环境下仍可正常构建。
func applyAutoStart(exePath string, enable bool) {
	// no-op on non-Windows
}
