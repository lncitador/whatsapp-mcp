package transcriber

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const longAudioThreshold = 10 * time.Minute

func ChunkAudio(audioPath string, chunkDuration time.Duration) ([]string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH")
	}

	if _, err := os.Stat(audioPath); err != nil {
		return nil, fmt.Errorf("audio file not found: %s", audioPath)
	}

	duration, err := probeDuration(audioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to probe audio duration: %v", err)
	}

	if duration <= longAudioThreshold {
		return []string{audioPath}, nil
	}

	tmpDir, err := os.MkdirTemp("", "chunk-*")
	if err != nil {
		return nil, err
	}

	chunkSec := int(chunkDuration.Seconds())
	if chunkSec <= 0 {
		chunkSec = 300
	}

	totalSec := int(duration.Seconds())
	chunks := (totalSec + chunkSec - 1) / chunkSec

	var paths []string
	for i := 0; i < chunks; i++ {
		start := i * chunkSec
		outPath := filepath.Join(tmpDir, fmt.Sprintf("chunk_%03d%s", i, filepath.Ext(audioPath)))

		cmd := exec.Command("ffmpeg", "-y",
			"-i", audioPath,
			"-ss", strconv.Itoa(start),
			"-t", strconv.Itoa(chunkSec),
			"-c", "copy",
			outPath,
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("ffmpeg chunk %d failed: %v: %s", i, err, output)
		}

		paths = append(paths, outPath)
	}

	return paths, nil
}

func probeDuration(audioPath string) (time.Duration, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		audioPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	sec, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration output: %s", output)
	}

	return time.Duration(sec * float64(time.Second)), nil
}

func CleanupChunks(paths []string) {
	for _, p := range paths {
		dir := filepath.Dir(p)
		os.RemoveAll(dir)
	}
}
