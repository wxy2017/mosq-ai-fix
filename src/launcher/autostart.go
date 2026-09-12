package main

import (
	"os"
	"path/filepath"
	"strings"
)

// readAutoStart 读取 <工具根>/config/typo_config.ini 中 [startup] auto_start。
// 默认返回 false（关闭）；文件缺失或解析失败也视为关闭。
// 该配置项控制是否将本程序写入 Windows 开机自启动（HKCU Run）。
func readAutoStart(rootDir string) bool {
	path := filepath.Join(rootDir, "config", "typo_config.ini")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	section := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		if section != "startup" {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		if key == "auto_start" {
			return strings.EqualFold(val, "true") || val == "1"
		}
	}
	return false
}
