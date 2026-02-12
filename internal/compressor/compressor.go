package compressor

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	TempFile    string
	DurationSec float64
}

type ResumeEntry struct {
	InputFile    string `json:"input_file"`
	OutputFile   string `json:"output_file"`
	InputSize    int64  `json:"input_size"`
	InputModUnix int64  `json:"input_mod_unix"`
	CompletedAt  string `json:"completed_at"`
}

type ResumeState struct {
	Completed map[string]ResumeEntry `json:"completed"`
}

func stateFilePath(cfg config.Config) string {
	if cfg.OutputPath != "" {
		return filepath.Join(cfg.OutputPath, ".vc-resume.json")
	}

	inputPath := cfg.InputPath
	if info, err := os.Stat(inputPath); err == nil && info.IsDir() {
		return filepath.Join(inputPath, ".vc-resume.json")
	}
	return filepath.Join(filepath.Dir(inputPath), ".vc-resume.json")
}

func loadResumeState(path string) (*ResumeState, error) {
	state := &ResumeState{Completed: map[string]ResumeEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	if state.Completed == nil {
		state.Completed = map[string]ResumeEntry{}
	}
	return state, nil
}

func saveResumeState(path string, state *ResumeState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func isCompletedAndUnchanged(state *ResumeState, inputFile, outputFile string) bool {
	entry, ok := state.Completed[inputFile]
	if !ok {
		return false
	}
	if entry.OutputFile != outputFile {
		return false
	}
	inputInfo, err := os.Stat(inputFile)
	if err != nil {
		return false
	}
	if inputInfo.Size() != entry.InputSize || inputInfo.ModTime().Unix() != entry.InputModUnix {
		return false
	}
	if _, err := os.Stat(outputFile); err != nil {
		return false
	}
	return true
}

func markCompleted(state *ResumeState, inputFile, outputFile string, inputSize int64, inputModUnix int64) {
	state.Completed[inputFile] = ResumeEntry{
		InputFile:    inputFile,
		OutputFile:   outputFile,
		InputSize:    inputSize,
		InputModUnix: inputModUnix,
		CompletedAt:  time.Now().Format(time.RFC3339),
	}
}

// ScanJobs 扫描文件
// 返回值: jobs, ignored, totalDuration, error
func ScanJobs(cfg config.Config) ([]Job, []ReportItem, float64, *ResumeState, string, error) {
	info, err := os.Stat(cfg.InputPath)
	if err != nil {
		return nil, nil, 0, nil, "", err
	}

	var jobs []Job
	var ignored []ReportItem
	var totalDuration float64
	statePath := stateFilePath(cfg)
	state, err := loadResumeState(statePath)
	if err != nil {
		return nil, nil, 0, nil, "", fmt.Errorf("load resume state failed: %w", err)
	}

	// 用于读取用户输入
	reader := bufio.NewReader(os.Stdin)

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
		if absPath, err := filepath.Abs(path); err == nil {
			path = absPath
		}

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

		// [新增功能] 检查输出文件是否存在并提示
		outputFile := getOutputPath(path)
		if isCompletedAndUnchanged(state, path, outputFile) {
			ignored = append(ignored, ReportItem{
				InputFile: path,
				Status:    "Ignored",
				Reason:    "Resume checkpoint: already completed",
			})
			return nil
		}

		if _, err := os.Stat(outputFile); err == nil {
			fmt.Printf("\n⚠️  目标文件已存在: %s\n", outputFile)
			fmt.Print("❓ 是否覆盖? (y/N): ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))

			if input != "y" && input != "yes" {
				ignored = append(ignored, ReportItem{
					InputFile: path,
					Status:    "Ignored",
					Reason:    "目标文件已存在 (用户选择跳过)",
				})
				return nil
			}
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
			OutputFile:  outputFile,
			TempFile:    outputFile + ".vcpart",
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
	return jobs, ignored, totalDuration, state, statePath, err
}

type segmentResumeMeta struct {
	InputFile      string  `json:"input_file"`
	OutputFile     string  `json:"output_file"`
	InputSize      int64   `json:"input_size"`
	InputModUnix   int64   `json:"input_mod_unix"`
	SegmentSeconds int     `json:"segment_seconds"`
	DurationSec    float64 `json:"duration_sec"`
	TotalSegments  int     `json:"total_segments"`
}

func shouldUseSegmentResume(cfg config.Config, jobs []Job) bool {
	return len(jobs) == 1 && !cfg.DisableSegResume
}

func segmentWorkDir(job Job) string {
	sum := sha1.Sum([]byte(job.InputFile + "|" + job.OutputFile))
	hash := hex.EncodeToString(sum[:])[:10]
	base := strings.TrimSuffix(filepath.Base(job.OutputFile), filepath.Ext(job.OutputFile))
	return filepath.Join(filepath.Dir(job.OutputFile), ".vcparts", fmt.Sprintf("%s-%s", base, hash))
}

func loadSegmentMeta(path string) (*segmentResumeMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta segmentResumeMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func saveSegmentMeta(path string, meta *segmentResumeMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func cleanupSegmentWorkspaceIfChanged(workDir string, expected segmentResumeMeta) error {
	metaPath := filepath.Join(workDir, "resume_meta.json")
	meta, err := loadSegmentMeta(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return os.RemoveAll(workDir)
	}

	if meta.InputFile != expected.InputFile ||
		meta.OutputFile != expected.OutputFile ||
		meta.InputSize != expected.InputSize ||
		meta.InputModUnix != expected.InputModUnix ||
		meta.SegmentSeconds != expected.SegmentSeconds ||
		meta.TotalSegments != expected.TotalSegments {
		return os.RemoveAll(workDir)
	}
	return nil
}

func segmentDurationAt(idx int, totalDuration float64, segmentSeconds int) float64 {
	start := float64(idx * segmentSeconds)
	left := totalDuration - start
	seg := float64(segmentSeconds)
	if left < seg {
		return left
	}
	return seg
}

func processSingleJobWithSegmentResume(
	ctx context.Context,
	j Job,
	cfg config.Config,
	globalBar *progressbar.ProgressBar,
	state *ResumeState,
	statePath string,
	stateMu *sync.Mutex,
) ReportItem {
	item := ReportItem{
		InputFile:  j.InputFile,
		OutputFile: j.OutputFile,
		Command:    fmt.Sprintf("ffmpeg segmented-resume input=%s output=%s", j.InputFile, j.OutputFile),
	}

	inputInfo, err := os.Stat(j.InputFile)
	if err != nil {
		item.Status = "Failed"
		item.Reason = fmt.Sprintf("stat input failed: %v", err)
		return item
	}
	item.OriginalSize = inputInfo.Size()

	segmentSeconds := cfg.SegmentSeconds
	if segmentSeconds <= 0 {
		segmentSeconds = 600
	}
	totalSegments := int(math.Ceil(j.DurationSec / float64(segmentSeconds)))
	if totalSegments < 1 {
		totalSegments = 1
	}

	workDir := segmentWorkDir(j)
	expectedMeta := segmentResumeMeta{
		InputFile:      j.InputFile,
		OutputFile:     j.OutputFile,
		InputSize:      inputInfo.Size(),
		InputModUnix:   inputInfo.ModTime().Unix(),
		SegmentSeconds: segmentSeconds,
		DurationSec:    j.DurationSec,
		TotalSegments:  totalSegments,
	}

	if err := cleanupSegmentWorkspaceIfChanged(workDir, expectedMeta); err != nil {
		item.Status = "Failed"
		item.Reason = fmt.Sprintf("cleanup segment workspace failed: %v", err)
		return item
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		item.Status = "Failed"
		item.Reason = fmt.Sprintf("create segment workspace failed: %v", err)
		return item
	}
	if err := saveSegmentMeta(filepath.Join(workDir, "resume_meta.json"), &expectedMeta); err != nil {
		item.Status = "Failed"
		item.Reason = fmt.Sprintf("save segment meta failed: %v", err)
		return item
	}

	for idx := 0; idx < totalSegments; idx++ {
		if ctx.Err() != nil {
			item.Status = "Failed"
			item.Reason = ctx.Err().Error()
			return item
		}

		startSec := float64(idx * segmentSeconds)
		durationSec := segmentDurationAt(idx, j.DurationSec, segmentSeconds)
		if durationSec <= 0 {
			continue
		}

		segmentFile := filepath.Join(workDir, fmt.Sprintf("seg_%06d.mp4", idx))
		segmentTmp := segmentFile + ".vcpart"

		if _, err := os.Stat(segmentFile); err == nil {
			if segDur, err := utils.GetVideoDuration(segmentFile); err == nil && segDur > 0 {
				_ = globalBar.Add64(int64(segDur * 1000000))
				continue
			}
			_ = os.Remove(segmentFile)
		}

		_ = os.Remove(segmentTmp)
		args := ffmpeg.BuildSegmentArgs(j.InputFile, segmentTmp, cfg, startSec, durationSec)
		if err := ffmpeg.Run(ctx, args, globalBar); err != nil {
			_ = os.Remove(segmentTmp)
			item.Status = "Failed"
			item.Reason = err.Error()
			return item
		}
		if err := os.Rename(segmentTmp, segmentFile); err != nil {
			_ = os.Remove(segmentTmp)
			item.Status = "Failed"
			item.Reason = fmt.Sprintf("finalize segment failed: %v", err)
			return item
		}
	}

	concatList := filepath.Join(workDir, "concat_list.txt")
	listFile, err := os.Create(concatList)
	if err != nil {
		item.Status = "Failed"
		item.Reason = fmt.Sprintf("create concat list failed: %v", err)
		return item
	}
	for idx := 0; idx < totalSegments; idx++ {
		segmentFile := filepath.Join(workDir, fmt.Sprintf("seg_%06d.mp4", idx))
		if _, err := os.Stat(segmentFile); err != nil {
			_ = listFile.Close()
			item.Status = "Failed"
			item.Reason = fmt.Sprintf("missing segment for concat: %s", filepath.Base(segmentFile))
			return item
		}
		escaped := strings.ReplaceAll(segmentFile, "'", "'\\''")
		if _, err := fmt.Fprintf(listFile, "file '%s'\n", escaped); err != nil {
			_ = listFile.Close()
			item.Status = "Failed"
			item.Reason = fmt.Sprintf("write concat list failed: %v", err)
			return item
		}
	}
	if err := listFile.Close(); err != nil {
		item.Status = "Failed"
		item.Reason = fmt.Sprintf("close concat list failed: %v", err)
		return item
	}

	finalTmp := j.OutputFile + ".merge.vcpart"
	_ = os.Remove(finalTmp)
	if err := ffmpeg.ConcatSegments(ctx, concatList, finalTmp); err != nil {
		_ = os.Remove(finalTmp)
		item.Status = "Failed"
		item.Reason = err.Error()
		return item
	}
	if err := os.Rename(finalTmp, j.OutputFile); err != nil {
		_ = os.Remove(finalTmp)
		item.Status = "Failed"
		item.Reason = fmt.Sprintf("finalize output failed: %v", err)
		return item
	}

	if info, err := os.Stat(j.OutputFile); err == nil {
		item.NewSize = info.Size()
	}
	item.Status = "Processed"

	stateMu.Lock()
	markCompleted(state, j.InputFile, j.OutputFile, inputInfo.Size(), inputInfo.ModTime().Unix())
	saveErr := saveResumeState(statePath, state)
	stateMu.Unlock()
	if saveErr != nil {
		globalBar.Clear()
		fmt.Printf("\n⚠️ 保存断点状态失败: %v\n", saveErr)
		_ = globalBar.RenderBlank()
	}

	_ = os.RemoveAll(workDir)
	return item
}

// Process 批量处理任务
func Process(ctx context.Context, jobs []Job, cfg config.Config, globalBar *progressbar.ProgressBar, state *ResumeState, statePath string) []ReportItem {
	var stateMu sync.Mutex
	if shouldUseSegmentResume(cfg, jobs) {
		return []ReportItem{
			processSingleJobWithSegmentResume(ctx, jobs[0], cfg, globalBar, state, statePath, &stateMu),
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Workers)

	results := make([]ReportItem, 0, len(jobs))
	var mu sync.Mutex

loop:
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Done()
			break loop
		}

		go func(j Job) {
			defer wg.Done()
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}

			var origSize int64
			var origModUnix int64
			if info, err := os.Stat(j.InputFile); err == nil {
				origSize = info.Size()
				origModUnix = info.ModTime().Unix()
			}

			_ = os.Remove(j.TempFile)
			args := ffmpeg.BuildArgs(j.InputFile, j.TempFile, cfg)
			cmdStr := fmt.Sprintf("ffmpeg %s", strings.Join(args, " "))

			err := ffmpeg.Run(ctx, args, globalBar)

			item := ReportItem{
				InputFile:    j.InputFile,
				OutputFile:   j.OutputFile,
				OriginalSize: origSize,
				Command:      cmdStr,
			}

			if err != nil {
				_ = os.Remove(j.TempFile)
				globalBar.Clear()
				if ctx.Err() != nil {
					fmt.Printf("\n⚠️ 已取消: %s\n", filepath.Base(j.InputFile))
				} else {
					fmt.Printf("\n❌ 失败: %s (%v)\n", filepath.Base(j.InputFile), err)
				}
				_ = globalBar.RenderBlank()
				item.Status = "Failed"
				item.Reason = err.Error()
			} else {
				if err := os.Rename(j.TempFile, j.OutputFile); err != nil {
					_ = os.Remove(j.TempFile)
					item.Status = "Failed"
					item.Reason = fmt.Sprintf("finalize output failed: %v", err)
				} else {
					item.Status = "Processed"
					stateMu.Lock()
					markCompleted(state, j.InputFile, j.OutputFile, origSize, origModUnix)
					saveErr := saveResumeState(statePath, state)
					stateMu.Unlock()
					if saveErr != nil {
						globalBar.Clear()
						fmt.Printf("\n⚠️ 保存断点状态失败: %v\n", saveErr)
						_ = globalBar.RenderBlank()
					}
				}
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
