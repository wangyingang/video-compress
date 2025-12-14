package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"video-compress/internal/compressor"
	"video-compress/internal/config"

	"github.com/schollz/progressbar/v3"
	"github.com/spf13/pflag"
)

func main() {
	// 1. 参数解析
	var outputDir, presetName string
	var customQuality, workers int

	pflag.StringVarP(&outputDir, "output", "o", "", "指定输出目录")
	pflag.StringVarP(&presetName, "preset", "p", config.PresetStandard, "压缩预设: high, standard, low")
	pflag.IntVarP(&customQuality, "quality", "q", 0, "自定义质量 (1-100)")
	pflag.IntVarP(&workers, "workers", "w", 2, "并发处理数量")
	pflag.Parse()

	if len(pflag.Args()) == 0 {
		fmt.Println("Usage: video-compress <input_file_or_dir> [flags]")
		pflag.PrintDefaults()
		os.Exit(1)
	}

	cfg := config.Config{
		InputPath:  pflag.Args()[0],
		OutputPath: outputDir,
		Preset:     strings.ToLower(presetName),
		Quality:    customQuality,
		Workers:    workers,
	}

	// 2. 扫描任务
	fmt.Println("正在扫描文件并分析时长...")
	jobs, totalDuration, err := compressor.ScanJobs(cfg)
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		os.Exit(1)
	}
	if len(jobs) == 0 {
		fmt.Println("未找到视频文件。")
		os.Exit(0)
	}
	if len(jobs) == 1 {
		cfg.Workers = 1
	}

	// 3. UI 初始化
	fmt.Println("------------------------------------------------")
	fmt.Printf("目标架构: Apple Silicon M2 Max\n")
	fmt.Printf("待处理文件: %d 个 (总时长: %.1f 小时)\n", len(jobs), totalDuration/3600)
	fmt.Printf("并发线程数: %d\n", cfg.Workers)
	fmt.Println("------------------------------------------------")

	bar := progressbar.NewOptions64(
		int64(totalDuration*1000000),
		progressbar.OptionSetDescription("总体进度"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetWidth(15),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer: "=", SaucerHead: ">", SaucerPadding: " ", BarStart: "[", BarEnd: "]",
		}),
	)
	_ = bar.RenderBlank()

	// 4. 信号监听
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		fmt.Println("\n\n⚠️ 用户中断，正在退出...")
		os.Exit(1)
	}()

	// 5. 执行
	start := time.Now()
	compressor.Process(jobs, cfg, bar)
	_ = bar.Finish()
	fmt.Printf("\n\n✅ 完成! 耗时: %s\n", time.Since(start).Round(time.Second))
}
