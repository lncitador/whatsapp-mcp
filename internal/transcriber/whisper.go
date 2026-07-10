package transcriber

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type WhisperCLI struct {
	Model  string
	Binary string
}

type whisperSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type whisperOutput struct {
	Segments []whisperSegment `json:"segments"`
}

func NewWhisperCLI(model string) *WhisperCLI {
	binary := "whisper-cli"
	if path, err := exec.LookPath("whisper-cli"); err == nil {
		binary = path
	}
	return &WhisperCLI{Model: model, Binary: binary}
}

func (w *WhisperCLI) Available() bool {
	_, err := exec.LookPath("whisper-cli")
	return err == nil
}

func (w *WhisperCLI) Transcribe(mediaPath string) (*Result, error) {
	if _, err := os.Stat(mediaPath); err != nil {
		return nil, fmt.Errorf("media file not found: %s", mediaPath)
	}

	tmpDir, err := os.MkdirTemp("", "whisper-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command(w.Binary,
		"--model", w.Model,
		"--output_format", "json",
		"--output_dir", tmpDir,
		mediaPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("whisper-cli failed: %v: %s", err, output)
	}

	base := filepath.Base(mediaPath)
	ext := filepath.Ext(base)
	jsonFile := filepath.Join(tmpDir, base[:len(base)-len(ext)]+".json")

	data, err := os.ReadFile(jsonFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read whisper output: %v", err)
	}

	var wo whisperOutput
	if err := json.Unmarshal(data, &wo); err != nil {
		return nil, fmt.Errorf("failed to parse whisper output: %v", err)
	}

	result := &Result{}
	for _, seg := range wo.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: time.Duration(seg.Start * float64(time.Second)),
			End:   time.Duration(seg.End * float64(time.Second)),
			Text:  seg.Text,
		})
		result.Text += seg.Text + " "
	}

	return result, nil
}
