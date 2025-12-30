package compressor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"video-compress/internal/config"
	"video-compress/internal/ffmpeg"
	"video-compress/internal/utils"

	"github.com/schollz/progressbar/v3"
)

// ReportItem 存储单个文件的处理结果
type ReportItem struct {
	InputFile    string
	OutputFile   string
	Status       string // Processed, Ignored, Failed
	Reason       string // Ignored 或 Failed 的原因
	OriginalSize int64
	NewSize      int64
	Command      string
}

type Job struct {
	InputFile   string
	OutputFile  string
	DurationSec float64
}

// ScanJobs 扫描文件
// 返回值: jobs, ignored, totalDuration, error
func ScanJobs(cfg config.Config) ([]Job, []ReportItem, float64, error) {
	info, err := os.Stat(cfg.InputPath)
	if err != nil {
		return nil, nil, 0, err
	}

	var jobs []Job
	var ignored []ReportItem
	var totalDuration float64

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

	addFile := func(path string) error {
		ext := filepath.Ext(path)
		nameWithoutExt := strings.TrimSuffix(filepath.Base(path), ext)

		// 判断文件名是否以 .compressed 结尾 (忽略大小写)
		if strings.HasSuffix(strings.ToLower(nameWithoutExt), ".compressed") {
			ignored = append(ignored, ReportItem{
				InputFile: path,
				Status:    "Ignored",
				Reason:    "Filename indicates already compressed",
			})
			return nil
		}

		dur, err := utils.GetVideoDuration(path)
		if err != nil {
			fmt.Printf("⚠️ 警告: 无法读取文件信息，跳过: %s\n", filepath.Base(path))
			ignored = append(ignored, ReportItem{
				InputFile: path,
				Status:    "Failed",
				Reason:    fmt.Sprintf("Read info failed: %v", err),
			})
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
		err = filepath.Walk(cfg.InputPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				ext := strings.ToLower(filepath.Ext(path))
				if ext == ".mp4" || ext == ".mkv" || ext == ".mov" {
					_ = addFile(path)
				}
			}
			return nil
		})
	}
	return jobs, ignored, totalDuration, err
}

// Process 批量处理任务
func Process(jobs []Job, cfg config.Config, globalBar *progressbar.ProgressBar) []ReportItem {
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Workers)

	results := make([]ReportItem, 0, len(jobs))
	var mu sync.Mutex

	for _, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}

		go func(j Job) {
			defer wg.Done()
			defer func() { <-sem }()

			var origSize int64
			if info, err := os.Stat(j.InputFile); err == nil {
				origSize = info.Size()
			}

			args := ffmpeg.BuildArgs(j.InputFile, j.OutputFile, cfg)
			cmdStr := fmt.Sprintf("ffmpeg %s", strings.Join(args, " "))

			err := ffmpeg.Run(args, globalBar)

			item := ReportItem{
				InputFile:    j.InputFile,
				OutputFile:   j.OutputFile,
				OriginalSize: origSize,
				Command:      cmdStr,
			}

			if err != nil {
				globalBar.Clear()
				fmt.Printf("\n❌ 失败: %s (%v)\n", filepath.Base(j.InputFile), err)
				_ = globalBar.RenderBlank()
				item.Status = "Failed"
				item.Reason = err.Error()
			} else {
				item.Status = "Processed"
				if info, err := os.Stat(j.OutputFile); err == nil {
					item.NewSize = info.Size()
				}
			}

			mu.Lock()
			results = append(results, item)
			mu.Unlock()

		}(job)
	}
	wg.Wait()
	return results
}
