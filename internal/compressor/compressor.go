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

type Job struct {
	InputFile   string
	OutputFile  string
	DurationSec float64
}

// ScanJobs 扫描文件
func ScanJobs(cfg config.Config) ([]Job, float64, error) {
	info, err := os.Stat(cfg.InputPath)
	if err != nil {
		return nil, 0, err
	}

	var jobs []Job
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
		dur, err := utils.GetVideoDuration(path)
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
		err = filepath.Walk(cfg.InputPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
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
	}
	return jobs, totalDuration, err
}

// Process 批量处理任务
func Process(jobs []Job, cfg config.Config, globalBar *progressbar.ProgressBar) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Workers)

	for _, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}

		go func(j Job) {
			defer wg.Done()
			defer func() { <-sem }()

			args := ffmpeg.BuildArgs(j.InputFile, j.OutputFile, cfg)
			err := ffmpeg.Run(args, globalBar)

			if err != nil {
				globalBar.Clear()
				fmt.Printf("\n❌ 失败: %s (%v)\n", filepath.Base(j.InputFile), err)
				_ = globalBar.RenderBlank()
			}
		}(job)
	}
	wg.Wait()
}
