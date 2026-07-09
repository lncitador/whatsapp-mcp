package audio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ConvertToOpusOggTemp converts inputPath to a temporary .ogg (opus, 32k,
// 24000 Hz) and returns the temp file path. Caller removes the file.
func ConvertToOpusOggTemp(inputPath string) (string, error) {
	if _, err := os.Stat(inputPath); err != nil {
		return "", fmt.Errorf("input file not found: %s", inputPath)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH — install ffmpeg or provide an .ogg opus file")
	}
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	tmp, err := os.CreateTemp("", base+"-*.ogg")
	if err != nil {
		return "", err
	}
	tmp.Close()
	cmd := exec.Command("ffmpeg", "-y",
		"-i", inputPath,
		"-c:a", "libopus",
		"-b:a", "32k",
		"-ar", "24000",
		"-application", "voip",
		tmp.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("ffmpeg conversion failed: %v: %s", err, out)
	}
	return tmp.Name(), nil
}
