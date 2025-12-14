package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schollz/progressbar/v3"
	"github.com/spf13/pflag"
)

// 定义预设枚举
const (
	PresetHigh     = "high"
	PresetStandard = "standard"
	PresetLow      = "low"
)

// Config 存储运行时参数
type Config struct {
	InputPath   string 
	OutputPath  string
	Preset      string
	Quality     int
	Workers     int 
}

// Job 代表一个压缩任务
type Job struct {
	InputFile   string
	OutputFile  string
	DurationSec float64 // 新增：预先获取的视频时长
}

func main() {
	// 1. 解析参数
	var outputDir string
	var presetName string
	var customQuality int
	var workers int

	pflag.StringVarP(&outputDir, "output", "o", "", "指定输出目录")
	pflag.StringVarP(&presetName, "preset", "p", PresetStandard, "压缩预设: high, standard, low")
	pflag.IntVarP(&customQuality, "quality", "q", 0, "自定义质量 (1-100)")
	pflag.IntVarP(&workers, "workers", "w", 2, "并发处理数量")
	pflag.Parse()

	args := pflag.Args()
	if len(args) == 0 {
		fmt.Println("Usage: video-compress <input_file_or_dir> [flags]")
		pflag.PrintDefaults()
		os.Exit(1)
	}

	config := Config{
		InputPath:  args[0],
		OutputPath: outputDir,
		Preset:     strings.ToLower(presetName),
		Quality:    customQuality,
		Workers:    workers,
	}

	// 2. 扫描文件并预计算总时长
	fmt.Println("正在扫描文件并分析时长...")
	jobs, totalDurationSec, err := scanJobs(config)
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		os.Exit(1)
	}

	if len(jobs) == 0 {
		fmt.Println("未找到支持的视频文件。")
		os.Exit(0)
	}

	// 智能单线程策略
	if len(jobs) == 1 {
		config.Workers = 1
	}

	// 3. 打印任务概览
	fmt.Println("------------------------------------------------")
	fmt.Printf("目标架构: Apple Silicon M2 Max (全链路硬件加速 Enabled)\n")
	fmt.Printf("待处理文件: %d 个 (总时长: %.1f 小时)\n", len(jobs), totalDurationSec/3600)
	fmt.Printf("并发线程数: %d\n", config.Workers)
	fmt.Printf("预设模式: %s (Q: %d)\n", config.Preset, config.Quality)
	fmt.Println("------------------------------------------------")

	// 4. 初始化全局进度条 (以微秒为单位)
	// 使用 totalDurationSec * 1,000,000
	mainBar := progressbar.NewOptions64(
		int64(totalDurationSec*1000000),
		progressbar.OptionSetDescription("总体进度"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetWidth(15),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(), // 显示已处理时间/总时间
		progressbar.OptionOnCompletion(func() {
			fmt.Fprint(os.Stderr, "\n")
		}),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	_ = mainBar.RenderBlank() // 强制渲染初始状态

	// 5. 并发控制
	var wg sync.WaitGroup
	sem := make(chan struct{}, config.Workers)
	
	// 监听中断
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n\n⚠️  用户强制中断，正在停止...")
		os.Exit(1)
	}()

	startTime := time.Now()

	for i, job := range jobs {
		wg.Add(1)
		sem <- struct{}{} 

		go func(id int, j Job) {
			defer wg.Done()
			defer func() { <-sem }() 

			// 传递 mainBar 给处理函数，以便实时更新
			err := processVideo(j, config, mainBar)
			if err != nil {
				// 打印错误前先清除进度条当前行，防止乱码
				mainBar.Clear()
				fmt.Printf("\n❌ 失败: %s (%v)\n", filepath.Base(j.InputFile), err)
				_ = mainBar.RenderBlank()
			}
		}(i, job)
	}

	wg.Wait()
	_ = mainBar.Finish()

	fmt.Printf("\n\n✅ 所有任务完成! 总耗时: %s\n", time.Since(startTime).Round(time.Second))
}

// processVideo 改为接收全局进度条指针
func processVideo(job Job, cfg Config, globalBar *progressbar.ProgressBar) error {
	cmdArgs := buildFFmpegArgs(job.InputFile, job.OutputFile, cfg)
	cmd := exec.Command("ffmpeg", cmdArgs...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil { return err }
	if err := cmd.Start(); err != nil { return err }

	// 实时解析进度
	scanner := bufio.NewScanner(stdoutPipe)
	
	// 记录上一次读取的时间点，用于计算增量
	var lastTimeUs int64 = 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "out_time_us=") {
			usStr := strings.TrimPrefix(line, "out_time_us=")
			currentUs, _ := strconv.ParseInt(usStr, 10, 64)
			
			// 计算增量 (Delta)
			// 注意：因为是并发，多个线程会同时向 globalBar Add，这是线程安全的
			if currentUs > lastTimeUs {
				delta := currentUs - lastTimeUs
				_ = globalBar.Add64(delta)
				lastTimeUs = currentUs
			}
		}
	}

	return cmd.Wait()
}

// scanJobs 扫描并计算总时长
func scanJobs(cfg Config) ([]Job, float64, error) {
	info, err := os.Stat(cfg.InputPath)
	if err != nil { return nil, 0, err }

	var jobs []Job
	var totalDuration float64 = 0

	getOutputPath := func(input string) string {
		ext := filepath.Ext(input)
		name := strings.TrimSuffix(filepath.Base(input), ext)
		targetDir := filepath.Dir(input)
		if cfg.OutputPath != "" {
			targetDir = cfg.OutputPath
			_ = os.MkdirAll(targetDir, 0755)
		}
		return filepath.Join(targetDir, fmt.Sprintf("%s.compressed%s", name, ext))
	}

	// 定义处理单个文件的逻辑
	addFile := func(path string) error {
		// 获取时长 (必须步骤)
		dur, err := getVideoDuration(path)
		if err != nil {
			fmt.Printf("⚠️ 警告: 无法读取文件信息，跳过: %s\n", filepath.Base(path))
			return nil
		}
		
		jobs = append(jobs, Job{
			InputFile:   path,
			OutputFile:  getOutputPath(path),
			DurationSec: dur,
		})
		totalDuration += dur
		return nil
	}

	if !info.IsDir() {
		_ = addFile(cfg.InputPath)
	} else {
		err := filepath.Walk(cfg.InputPath, func(path string, info os.FileInfo, err error) error {
			if err != nil { return err }
			if !info.IsDir() {
				ext := strings.ToLower(filepath.Ext(path))
				if ext == ".mp4" || ext == ".mkv" || ext == ".mov" {
					if !strings.Contains(path, ".compressed.") {
						_ = addFile(path)
					}
				}
			}
			return nil
		})
		if err != nil { return nil, 0, err }
	}
	return jobs, totalDuration, nil
}

// buildFFmpegArgs (保持不变)
func buildFFmpegArgs(inputFile, outputFile string, cfg Config) []string {
	args := []string{
		"-y", "-hwaccel", "videotoolbox", "-i", inputFile,
		"-progress", "pipe:1", "-nostats", "-hide_banner",
		"-map_metadata", "0", "-movflags", "+faststart",
	}

	qValue := "58"
	if cfg.Quality > 0 {
		qValue = strconv.Itoa(cfg.Quality)
	} else if cfg.Preset == PresetLow {
		qValue = "45"
	} else if cfg.Preset == PresetStandard {
		qValue = "58"
	}

	switch cfg.Preset {
	case PresetHigh:
		crf := "28"
		if cfg.Quality > 0 {
			mappedCRF := 51 - (cfg.Quality / 2)
			if mappedCRF < 0 { mappedCRF = 0 }
			crf = strconv.Itoa(mappedCRF)
		}
		args = append(args,
			"-c:v", "libx265", "-crf", crf, "-preset", "medium",
			"-pix_fmt", "yuv420p10le", "-tag:v", "hvc1",
			"-c:a", "aac", "-b:a", "128k",
		)
	case PresetLow:
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

// getVideoDuration (保持不变)
func getVideoDuration(filePath string) (float64, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filePath).Output()
	if err != nil { return 0, err }
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}
