package transcriber

import "time"

type Segment struct {
	Start time.Duration `json:"start"`
	End   time.Duration `json:"end"`
	Text  string        `json:"text"`
}

type Frame struct {
	Timestamp time.Duration `json:"timestamp"`
	Path      string        `json:"path"`
}

type Result struct {
	Text            string    `json:"text"`
	Segments        []Segment `json:"segments"`
	Frames          []Frame   `json:"frames,omitempty"`
	MarkdownPath    string    `json:"markdown_path,omitempty"`
	MarkdownContent string    `json:"markdown_content,omitempty"`
}

type Transcriber interface {
	Transcribe(mediaPath string) (*Result, error)
	Available() bool
}
