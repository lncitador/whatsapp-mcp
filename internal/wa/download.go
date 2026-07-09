package wa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"

	"github.com/lncitador/whatsapp-mcp/internal/config"
)

type MediaDownloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

func (d *MediaDownloader) GetDirectPath() string     { return d.DirectPath }
func (d *MediaDownloader) GetURL() string            { return d.URL }
func (d *MediaDownloader) GetMediaKey() []byte       { return d.MediaKey }
func (d *MediaDownloader) GetFileLength() uint64     { return d.FileLength }
func (d *MediaDownloader) GetFileSHA256() []byte     { return d.FileSHA256 }
func (d *MediaDownloader) GetFileEncSHA256() []byte  { return d.FileEncSHA256 }
func (d *MediaDownloader) GetMediaType() whatsmeow.MediaType { return d.MediaType }

func extractDirectPathFromURL(url string) string {
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url
	}
	return "/" + parts[1]
}

func (c *Client) DownloadMedia(messageID, chatJID string) (string, string, string, error) {
	mi, err := c.st.GetMediaInfo(messageID, chatJID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to find message: %v", err)
	}

	if mi.MediaType == "" {
		return "", "", "", fmt.Errorf("not a media message")
	}

	chatDir := filepath.Join(config.MediaDir(), strings.ReplaceAll(chatJID, ":", "_"))
	if err := os.MkdirAll(chatDir, 0755); err != nil {
		return "", "", "", fmt.Errorf("failed to create chat directory: %v", err)
	}

	localPath := filepath.Join(chatDir, mi.Filename)
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	if _, err := os.Stat(localPath); err == nil {
		return absPath, mi.MediaType, mi.Filename, nil
	}

	if mi.URL == "" || len(mi.MediaKey) == 0 || len(mi.FileSHA256) == 0 || len(mi.FileEncSHA256) == 0 || mi.FileLength == 0 {
		return "", "", "", fmt.Errorf("incomplete media information for download")
	}

	c.logger.Debugf("Attempting to download media for message %s in chat %s", messageID, chatJID)

	directPath := extractDirectPathFromURL(mi.URL)

	var waMediaType whatsmeow.MediaType
	switch mi.MediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	default:
		return "", "", "", fmt.Errorf("unsupported media type: %s", mi.MediaType)
	}

	downloader := &MediaDownloader{
		URL:           mi.URL,
		DirectPath:    directPath,
		MediaKey:      mi.MediaKey,
		FileLength:    mi.FileLength,
		FileSHA256:    mi.FileSHA256,
		FileEncSHA256: mi.FileEncSHA256,
		MediaType:     waMediaType,
	}

	mediaData, err := c.wm.Download(context.Background(), downloader)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to download media: %v", err)
	}

	if err := os.WriteFile(localPath, mediaData, 0644); err != nil {
		return "", "", "", fmt.Errorf("failed to save media file: %v", err)
	}

	c.logger.Infof("Successfully downloaded %s media to %s (%d bytes)", mi.MediaType, absPath, len(mediaData))
	return absPath, mi.MediaType, mi.Filename, nil
}
