package main

import (
	"testing"
	"video-compress/internal/compressor"
)

func TestSummarizeScanItems(t *testing.T) {
	items := []compressor.ReportItem{
		{Status: "Ignored", Reason: "Filename indicates already compressed"},
		{Status: "Ignored", Reason: "目标文件已存在 (用户选择跳过)"},
		{Status: "Failed", Reason: "Read info failed: signal: abort trap"},
	}

	ignored, failed, compressedIgnored := summarizeScanItems(items)
	if ignored != 2 {
		t.Fatalf("expected ignored=2, got %d", ignored)
	}
	if failed != 1 {
		t.Fatalf("expected failed=1, got %d", failed)
	}
	if compressedIgnored != 1 {
		t.Fatalf("expected compressedIgnored=1, got %d", compressedIgnored)
	}
}
