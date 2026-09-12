//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// 仅 Windows 生效：通过 HKCU\...\Run 实现登录时自启动，无需管理员权限。
const (
	autoStartHKCU       = 0x80000001 // HKEY_CURRENT_USER
	autoStartSetValue   = 0x0002     // KEY_SET_VALUE
	autoStartQueryValue = 0x0001     // KEY_QUERY_VALUE
	autoStartRegSz      = 1          // REG_SZ
	autoStartRunKey     = `Software\Microsoft\Windows\CurrentVersion\Run`
	autoStartValueName  = "MosqAiFix"
)

var advapi32 = syscall.NewLazyDLL("advapi32.dll")
var procRegOpenKeyExW = advapi32.NewProc("RegOpenKeyExW")
var procRegSetValueExW = advapi32.NewProc("RegSetValueExW")
var procRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")
var procRegCloseKey = advapi32.NewProc("RegCloseKey")

// applyAutoStart 根据 enable 在 HKCU Run 中写入/删除本程序的启动项。
// 写入的值为带引号的 exe 绝对路径（路径含空格也能正确启动）。
func applyAutoStart(exePath string, enable bool) {
	if runtime.GOOS != "windows" {
		return
	}
	if exePath == "" {
		return
	}
	subKey := utf16Ptr(autoStartRunKey)
	var hk uintptr
	r, _, _ := procRegOpenKeyExW.Call(
		uintptr(autoStartHKCU),
		uintptr(unsafe.Pointer(subKey)),
		0,
		uintptr(autoStartSetValue|autoStartQueryValue),
		uintptr(unsafe.Pointer(&hk)),
	)
	if r != 0 {
		return // 打开失败（如权限不足），静默忽略
	}
	defer procRegCloseKey.Call(uintptr(hk))

	valName := utf16Ptr(autoStartValueName)
	if !enable {
		procRegDeleteValueW.Call(uintptr(hk), uintptr(unsafe.Pointer(valName)))
		return
	}
	// 写入带引号的绝对路径，确保路径含空格时也能被正确解析
	quoted := `"` + exePath + `"`
	u := utf16.Encode([]rune(quoted))
	u = append(u, 0)
	cb := uint32(len(u) * 2)
	procRegSetValueExW.Call(
		uintptr(hk),
		uintptr(unsafe.Pointer(valName)),
		0,
		uintptr(autoStartRegSz),
		uintptr(unsafe.Pointer(&u[0])),
		uintptr(cb),
	)
}

// utf16Ptr 将字符串转为 UTF-16 指针（含结尾 0），跨平台可用。
func utf16Ptr(s string) *uint16 {
	u := utf16.Encode([]rune(s + "\x00"))
	return &u[0]
}
