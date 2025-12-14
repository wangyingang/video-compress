package ffmpeg

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"video-compress/internal/config"

	"github.com/schollz/progressbar/v3"
)

// BuildArgs 构建 FFmpeg 参数
func BuildArgs(inputFile, outputFile string, cfg config.Config) []string {
	args := []string{
		"-y", "-hwaccel", "videotoolbox", "-i", inputFile,
		"-progress", "pipe:1", "-nostats", "-hide_banner",
		"-map_metadata", "0", "-movflags", "+faststart",
	}

	// 设置压缩质量，standard下为50,low为40,high采用软件压缩，并通过一个简单的换算法将
	// q:v 值转为crf值，具体如下
	qValue := "50"
	if cfg.Quality > 0 {
		qValue = strconv.Itoa(cfg.Quality)
	} else if cfg.Preset == config.PresetLow {
		qValue = "40"
	} else if cfg.Preset == config.PresetStandard {
		qValue = "50"
	}

	switch cfg.Preset {
	case config.PresetHigh:
		crf := "24"
		if cfg.Quality > 0 {
			mappedCRF := 51 - (cfg.Quality / 2)
			if mappedCRF < 0 {
				mappedCRF = 0
			}
			crf = strconv.Itoa(mappedCRF)
		}
		args = append(args,
			"-c:v", "libx265", "-crf", crf, "-preset", "medium",
			"-pix_fmt", "yuv420p10le", "-tag:v", "hvc1",
			"-c:a", "aac", "-b:a", "128k",
		)
	case config.PresetLow:
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-q:v", qValue,
			"-profile:v", "main10", "-tag:v", "hvc1", "-pix_fmt", "p010le",
			"-c:a", "aac", "-b:a", "96k",
		)
	default:
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-q:v", qValue,
			"-profile:v", "main10", "-tag:v", "hvc1", "-pix_fmt", "p010le",
			"-c:a", "aac", "-b:a", "128k",
		)
	}
	args = append(args, outputFile)
	return args
}

// Run 执行 FFmpeg 命令并更新进度条
func Run(cmdArgs []string, globalBar *progressbar.ProgressBar) error {
	cmd := exec.Command("ffmpeg", cmdArgs...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdoutPipe)
	var lastTimeUs int64 = 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "out_time_us=") {
			usStr := strings.TrimPrefix(line, "out_time_us=")
			currentUs, _ := strconv.ParseInt(usStr, 10, 64)

			if currentUs > lastTimeUs {
				delta := currentUs - lastTimeUs
				_ = globalBar.Add64(delta)
				lastTimeUs = currentUs
			}
		}
	}
	return cmd.Wait()
}
