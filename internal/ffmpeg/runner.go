package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"video-compress/internal/config"

	"github.com/schollz/progressbar/v3"
)

// BuildArgs 构建 FFmpeg 参数
func BuildArgs(inputFile, outputFile string, cfg config.Config) []string {
	// 1. 基础参数
	args := []string{"-y"}

	// 2. 硬件加速策略
	// 尝试启用 videotoolbox 硬件解码。
	// 注意：对于某些损坏严重的视频，FFmpeg 可能会自动回退到 h264(native) 软件解码，
	// 因此后续的滤镜链必须能同时处理硬件和软件两种输出。
	args = append(args, "-hwaccel", "videotoolbox")

	// 3. 通用输入参数
	args = append(args,
		"-i", inputFile,
		"-progress", "pipe:1", "-nostats", "-hide_banner",
		"-map_metadata", "0", "-movflags", "+faststart",
		"-ignore_unknown",           // 忽略无效流
		"-err_detect", "ignore_err", // [新增] 遇到数据损坏时尝试继续，而不是立即崩溃
	)

	// 计算质量参数
	qValue := "50"
	if cfg.Quality > 0 {
		qValue = strconv.Itoa(cfg.Quality)
	} else if cfg.Preset == config.PresetLow {
		qValue = "40"
	} else if cfg.Preset == config.PresetStandard {
		qValue = "50"
	}

	// 4. 视频编码配置
	switch cfg.Preset {
	case config.PresetHigh:
		// [High 模式] 混合流水线 (兼容模式)
		crf := "24"
		if cfg.Quality > 0 {
			mappedCRF := 51 - (cfg.Quality / 2)
			if mappedCRF < 0 {
				mappedCRF = 0
			}
			crf = strconv.Itoa(mappedCRF)
		}
		args = append(args,
			"-c:v", "libx265",
			"-crf", crf,
			"-preset", "medium",
			// [关键修改]
			// 移除 hwdownload，仅使用 format=yuv420p。
			// 原因：如果硬件解码失败回退到软件解码(nv12)，显式的 hwdownload 会导致崩溃。
			// format=yuv420p 更加智能：
			// 1. 若是硬件流，它会自动插入下载步骤。
			// 2. 若是软件流，它直接转换格式。
			"-vf", "format=yuv420p",
			"-tag:v", "hvc1",
		)
	case config.PresetLow:
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-q:v", qValue,
			"-profile:v", "main10", "-tag:v", "hvc1", "-pix_fmt", "p010le",
		)
	default:
		// Standard 模式
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-q:v", qValue,
			"-profile:v", "main10", "-tag:v", "hvc1", "-pix_fmt", "p010le",
		)
	}

	// 5. 音频处理
	// 统一使用流复制，避免解码错误并保持原音质
	args = append(args, "-c:a", "copy")

	args = append(args, outputFile)
	return args
}

// BuildSegmentArgs 构建单个时间分片的压缩参数
func BuildSegmentArgs(inputFile, outputFile string, cfg config.Config, startSec, durationSec float64) []string {
	args := []string{"-y"}
	args = append(args, "-hwaccel", "videotoolbox")
	args = append(args,
		"-ss", fmt.Sprintf("%.3f", startSec),
		"-t", fmt.Sprintf("%.3f", durationSec),
		"-i", inputFile,
		"-progress", "pipe:1", "-nostats", "-hide_banner",
		"-map_metadata", "0", "-movflags", "+faststart",
		"-ignore_unknown",
		"-err_detect", "ignore_err",
	)

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
			"-c:v", "libx265",
			"-crf", crf,
			"-preset", "medium",
			"-vf", "format=yuv420p",
			"-tag:v", "hvc1",
		)
	case config.PresetLow:
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-q:v", qValue,
			"-profile:v", "main10", "-tag:v", "hvc1", "-pix_fmt", "p010le",
		)
	default:
		args = append(args,
			"-c:v", "hevc_videotoolbox", "-q:v", qValue,
			"-profile:v", "main10", "-tag:v", "hvc1", "-pix_fmt", "p010le",
		)
	}

	// 分片模式统一重编码音频，保证分片拼接兼容性与时间连续性
	args = append(args, "-c:a", "aac", "-b:a", "160k")
	args = append(args, outputFile)
	return args
}

// Run 执行 FFmpeg 命令并更新进度条 (保持不变)
func Run(ctx context.Context, cmdArgs []string, globalBar *progressbar.ProgressBar) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", cmdArgs...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

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

	if err := cmd.Wait(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		fmt.Fprintf(os.Stderr, "\n\n❌ FFmpeg 运行错误日志:\n%s\n", stderr.String())
		return err
	}
	return nil
}

// ConcatSegments 将分片文件无损拼接为最终文件
func ConcatSegments(ctx context.Context, listFile, outputFile string) error {
	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", listFile,
		"-c", "copy",
		"-movflags", "+faststart",
		outputFile,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		fmt.Fprintf(os.Stderr, "\n\n❌ FFmpeg 拼接错误日志:\n%s\n", stderr.String())
		return err
	}
	return nil
}
