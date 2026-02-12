package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	// "text/tabwriter" // [已移除] 不再需要表格库
	"time"
	"video-compress/internal/compressor"
	"video-compress/internal/config"
	"video-compress/internal/ffmpeg"

	"github.com/schollz/progressbar/v3"
	"github.com/spf13/pflag"
)

func main() {
	// 1. 参数解析
	var outputDir, presetName string
	var customQuality, workers int
	var segmentSeconds int
	var disableSegmentResume bool

	pflag.StringVarP(&outputDir, "output", "o", "", "指定输出目录")
	pflag.StringVarP(&presetName, "preset", "p", config.PresetStandard, "压缩预设: high, standard, low")
	pflag.IntVarP(&customQuality, "quality", "q", 0, "自定义质量 (1-100)")
	pflag.IntVarP(&workers, "workers", "w", 2, "并发处理数量")
	pflag.IntVar(&segmentSeconds, "segment-seconds", 600, "单文件断点续传分片时长(秒)")
	pflag.BoolVar(&disableSegmentResume, "disable-segment-resume", false, "关闭单文件分片续传")
	pflag.Parse()

	if len(pflag.Args()) == 0 {
		fmt.Println("Usage: vc <input_file_or_dir> [flags]")
		pflag.PrintDefaults()
		os.Exit(1)
	}

	cfg := config.Config{
		InputPath:        pflag.Args()[0],
		OutputPath:       outputDir,
		Preset:           strings.ToLower(presetName),
		Quality:          customQuality,
		Workers:          workers,
		SegmentSeconds:   segmentSeconds,
		DisableSegResume: disableSegmentResume,
	}
	if cfg.SegmentSeconds <= 0 {
		cfg.SegmentSeconds = 600
	}

	// 2. 扫描任务
	fmt.Println("正在扫描文件并分析时长...")
	jobs, ignoredItems, totalDuration, resumeState, resumeStatePath, err := compressor.ScanJobs(cfg)
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		os.Exit(1)
	}

	if len(ignoredItems) > 0 {
		fmt.Printf("已忽略 %d 个不需要压缩的文件 (文件名包含 .compressed)\n", len(ignoredItems))
	}

	if len(jobs) == 0 {
		fmt.Println("未找到需要处理的视频文件。")
		printReport(nil, ignoredItems)
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

	if len(jobs) > 0 {
		sampleCmd := ffmpeg.BuildArgs(jobs[0].InputFile, jobs[0].OutputFile, cfg)
		fmt.Printf("执行命令预览: ffmpeg %s\n", strings.Join(sampleCmd, " "))
	}

	fmt.Println("------------------------------------------------")

	bar := progressbar.NewOptions64(
		int64(totalDuration*1000000),
		progressbar.OptionSetDescription("总体进度"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetWidth(20),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "█",
			SaucerHead:    "█",
			SaucerPadding: "░",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	_ = bar.RenderBlank()

	// 4. 信号监听
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	go func() {
		<-sig
		fmt.Println("\n\n⚠️ 用户中断，正在停止当前任务并保存断点...")
		cancel()
	}()

	// 5. 执行
	start := time.Now()
	processedItems := compressor.Process(ctx, jobs, cfg, bar, resumeState, resumeStatePath)
	_ = bar.Finish()

	// 6. 打印最终报告
	printReport(processedItems, ignoredItems)

	fmt.Printf("\n✅ 所有任务完成! 总耗时: %s\n", time.Since(start).Round(time.Second))
}

// printReport 打印任务总结报告 (列表模式)
// [修改] 改为列表展示，以便完整显示长文件名和命令
func printReport(processed, ignored []compressor.ReportItem) {
	fmt.Println("\n📊 任务处理报告")
	fmt.Println("================================================================================")

	formatSize := func(b int64) string {
		const unit = 1024
		if b < unit {
			return fmt.Sprintf("%d B", b)
		}
		div, exp := int64(unit), 0
		for n := b / unit; n >= unit; n /= unit {
			div *= unit
			exp++
		}
		return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
	}

	totalCount := len(processed) + len(ignored)
	index := 1

	// 1. 打印处理过的文件
	for _, item := range processed {
		// 显示完整文件名，不进行截断
		name := filepath.Base(item.InputFile)

		fmt.Printf("[%d/%d] 文件: %s\n", index, totalCount, name)

		if item.Status == "Failed" {
			fmt.Printf("    🔴 状态: 失败\n")
			fmt.Printf("    ❌ 原因: %s\n", item.Reason)
		} else {
			reduction := item.OriginalSize - item.NewSize
			percent := 0.0
			if item.OriginalSize > 0 {
				percent = (float64(reduction) / float64(item.OriginalSize)) * 100
			}

			fmt.Printf("    ✅ 状态: 完成\n")
			fmt.Printf("    📉 数据: %s -> %s (减少: %s / %.1f%%)\n",
				formatSize(item.OriginalSize),
				formatSize(item.NewSize),
				formatSize(reduction),
				percent,
			)
			// 显示完整命令
			fmt.Printf("    🛠  命令: %s\n", item.Command)
		}
		fmt.Println("--------------------------------------------------------------------------------")
		index++
	}

	// 2. 打印被忽略的文件
	for _, item := range ignored {
		name := filepath.Base(item.InputFile)
		fmt.Printf("[%d/%d] 文件: %s\n", index, totalCount, name)
		fmt.Printf("    ⚠️ 状态: 跳过\n")
		fmt.Printf("    📝 原因: %s\n", item.Reason)
		fmt.Println("--------------------------------------------------------------------------------")
		index++
	}

	// 3. 统计汇总
	successCount := 0
	failCount := 0
	for _, p := range processed {
		if p.Status == "Processed" {
			successCount++
		} else {
			failCount++
		}
	}

	fmt.Printf("统计: 总计 %d | 成功 %d | 失败 %d | 跳过 %d\n",
		totalCount, successCount, failCount, len(ignored))
	fmt.Println("================================================================================")
}
