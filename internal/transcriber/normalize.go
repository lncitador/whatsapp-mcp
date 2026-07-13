package transcriber

import (
	"fmt"
	"os"
	"os/exec"
)

// NormalizeToWAV converts mediaPath — any audio or video file ffmpeg can
// read — to a 16kHz mono 16-bit PCM WAV file. whisper.cpp's CLI only reads
// WAV in that exact format; WAV is also one of the few formats every
// backend here is guaranteed to accept (WhatsApp voice notes are Ogg
// Opus, which OpenAI's transcription API does not accept, and OpenAI's
// accepted formats don't overlap 100% with whisper.cpp's). Normalizing
// once, up front, means both backends and the chunker only ever deal with
// one known-good format. Caller must remove the returned path.
func NormalizeToWAV(mediaPath string) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH (required for transcription)")
	}

	tmpFile, err := os.CreateTemp("", "transcribe-*.wav")
	if err != nil {
		return "", err
	}
	tmpFile.Close()

	cmd := exec.Command("ffmpeg", "-y",
		"-i", mediaPath,
		"-vn", // drop any video stream — we only want audio
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		tmpFile.Name(),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("ffmpeg normalize to wav failed: %v: %s", err, output)
	}

	return tmpFile.Name(), nil
}
