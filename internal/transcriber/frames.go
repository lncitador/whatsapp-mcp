package transcriber

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func ExtractFrame(videoPath string, timestamp time.Duration) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH")
	}

	if _, err := os.Stat(videoPath); err != nil {
		return "", fmt.Errorf("video file not found: %s", videoPath)
	}

	tmpFile, err := os.CreateTemp("", "frame-*.jpg")
	if err != nil {
		return "", err
	}
	tmpFile.Close()

	cmd := exec.Command("ffmpeg", "-y",
		"-ss", timestamp.String(),
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "2",
		tmpFile.Name(),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("ffmpeg failed: %v: %s", err, output)
	}

	return tmpFile.Name(), nil
}

func ExtractFrames(videoPath string, segments []Segment, outputDir string) ([]Frame, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, err
	}

	var frames []Frame
	for i, seg := range segments {
		framePath := filepath.Join(outputDir, fmt.Sprintf("frame_%03d.jpg", i+1))

		cmd := exec.Command("ffmpeg", "-y",
			"-ss", seg.Start.String(),
			"-i", videoPath,
			"-frames:v", "1",
			"-q:v", "2",
			framePath,
		)

		if err := cmd.Run(); err != nil {
			continue
		}

		frames = append(frames, Frame{
			Timestamp: seg.Start,
			Path:      framePath,
		})
	}

	return frames, nil
}
