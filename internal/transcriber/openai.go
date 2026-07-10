package transcriber

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type OpenAITranscriber struct {
	APIKey string
	Model  string
}

type openaiSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type openaiResponse struct {
	Text     string          `json:"text"`
	Segments []openaiSegment `json:"segments,omitempty"`
}

func NewOpenAITranscriber() *OpenAITranscriber {
	return &OpenAITranscriber{
		APIKey: os.Getenv("OPENAI_API_KEY"),
		Model:  "whisper-1",
	}
}

func (o *OpenAITranscriber) Available() bool {
	return o.APIKey != ""
}

func (o *OpenAITranscriber) Transcribe(mediaPath string) (*Result, error) {
	if o.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	if _, err := os.Stat(mediaPath); err != nil {
		return nil, fmt.Errorf("media file not found: %s", mediaPath)
	}

	file, err := os.Open(mediaPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(mediaPath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, err
	}

	writer.WriteField("model", o.Model)
	writer.WriteField("response_format", "verbose_json")
	writer.Close()

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/audio/transcriptions", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, body)
	}

	var oar openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&oar); err != nil {
		return nil, err
	}

	result := &Result{Text: oar.Text}
	for _, seg := range oar.Segments {
		result.Segments = append(result.Segments, Segment{
			Start: time.Duration(seg.Start * float64(time.Second)),
			End:   time.Duration(seg.End * float64(time.Second)),
			Text:  seg.Text,
		})
	}

	return result, nil
}
