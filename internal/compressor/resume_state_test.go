package compressor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResumeStateSaveLoadAndMatch(t *testing.T) {
	t.Helper()

	tmpDir := t.TempDir()
	input := filepath.Join(tmpDir, "input.mp4")
	output := filepath.Join(tmpDir, "input.compressed.mp4")
	stateFile := filepath.Join(tmpDir, ".vc-resume.json")

	if err := os.WriteFile(input, []byte("source"), 0644); err != nil {
		t.Fatalf("write input failed: %v", err)
	}
	if err := os.WriteFile(output, []byte("result"), 0644); err != nil {
		t.Fatalf("write output failed: %v", err)
	}

	info, err := os.Stat(input)
	if err != nil {
		t.Fatalf("stat input failed: %v", err)
	}

	state := &ResumeState{Completed: map[string]ResumeEntry{}}
	markCompleted(state, input, output, info.Size(), info.ModTime().Unix())

	if err := saveResumeState(stateFile, state); err != nil {
		t.Fatalf("save state failed: %v", err)
	}

	loaded, err := loadResumeState(stateFile)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}

	if !isCompletedAndUnchanged(loaded, input, output) {
		t.Fatalf("expected resume match to be true")
	}
}

func TestResumeStateMismatchWhenInputChanges(t *testing.T) {
	t.Helper()

	tmpDir := t.TempDir()
	input := filepath.Join(tmpDir, "input.mp4")
	output := filepath.Join(tmpDir, "input.compressed.mp4")
	state := &ResumeState{Completed: map[string]ResumeEntry{}}

	if err := os.WriteFile(input, []byte("source-v1"), 0644); err != nil {
		t.Fatalf("write input failed: %v", err)
	}
	if err := os.WriteFile(output, []byte("result"), 0644); err != nil {
		t.Fatalf("write output failed: %v", err)
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatalf("stat input failed: %v", err)
	}
	markCompleted(state, input, output, info.Size(), info.ModTime().Unix())

	if err := os.WriteFile(input, []byte("source-v2-with-change"), 0644); err != nil {
		t.Fatalf("mutate input failed: %v", err)
	}
	if isCompletedAndUnchanged(state, input, output) {
		t.Fatalf("expected resume match to be false after input change")
	}
}
