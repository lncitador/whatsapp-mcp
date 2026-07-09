package audio

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAnalyzeOggOpusRejectsGarbage(t *testing.T) {
	if _, _, err := AnalyzeOggOpus([]byte("not an ogg")); err == nil {
		t.Fatal("want error for non-ogg data")
	}
}

func TestPlaceholderWaveformShape(t *testing.T) {
	wf := PlaceholderWaveform(30)
	if len(wf) != 64 {
		t.Fatalf("len = %d, want 64", len(wf))
	}
	for i, v := range wf {
		if v > 100 {
			t.Fatalf("waveform[%d] = %d out of 0-100", i, v)
		}
	}
}

func TestConvertMissingInput(t *testing.T) {
	if _, err := ConvertToOpusOggTemp("/nonexistent/file.mp3"); err == nil {
		t.Fatal("want error for missing input")
	}
}

// Integração real só quando ffmpeg existe no PATH.
func TestConvertWithFfmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// gera 1s de silêncio wav via ffmpeg e converte
	in := t.TempDir() + "/in.wav"
	if out, err := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono", "-t", "1", in).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	got, err := ConvertToOpusOggTemp(in)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(got)
	if !strings.HasSuffix(got, ".ogg") {
		t.Fatalf("output %q not .ogg", got)
	}
	data, _ := os.ReadFile(got)
	if len(data) < 4 || string(data[:4]) != "OggS" {
		t.Fatal("output is not an Ogg file")
	}
}
