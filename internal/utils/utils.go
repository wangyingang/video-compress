package utils

import (
	"os/exec"
	"strconv"
	"strings"
)

// GetVideoDuration 获取视频时长（秒）
func GetVideoDuration(filePath string) (float64, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filePath).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

// EnsureDir 确保目录存在
func EnsureDir(dir string) error {
	return exec.Command("mkdir", "-p", dir).Run()
}
