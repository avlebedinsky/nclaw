package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// attachment holds normalized file info extracted from a Telegram message.
type attachment struct {
	fileID       string
	fileUniqueID string
	fileSize     int64
	filename     string
	speech       bool
	duration     int
}

// extractAttachment returns file info if the message contains a file, or nil otherwise.
func extractAttachment(msg *models.Message) *attachment {
	if att := extractMedia(msg); att != nil {
		return att
	}

	return extractExtra(msg)
}

func extractMedia(msg *models.Message) *attachment {
	switch {
	case msg.Document != nil:
		d := msg.Document
		return &attachment{fileID: d.FileID, fileUniqueID: d.FileUniqueID, fileSize: d.FileSize, filename: d.FileName}
	case len(msg.Photo) > 0:
		p := msg.Photo[len(msg.Photo)-1]
		return &attachment{fileID: p.FileID, fileUniqueID: p.FileUniqueID, fileSize: int64(p.FileSize), filename: "photo.jpg"}
	case msg.Audio != nil:
		a := msg.Audio
		return &attachment{fileID: a.FileID, fileUniqueID: a.FileUniqueID, fileSize: a.FileSize, filename: nameOr(a.FileName, "audio.ogg")}
	case msg.Voice != nil:
		v := msg.Voice
		return &attachment{fileID: v.FileID, fileUniqueID: v.FileUniqueID, fileSize: v.FileSize, filename: "voice.ogg",
			speech: true, duration: v.Duration}
	default:
		return nil
	}
}

func extractExtra(msg *models.Message) *attachment {
	switch {
	case msg.Video != nil:
		v := msg.Video
		return &attachment{fileID: v.FileID, fileUniqueID: v.FileUniqueID, fileSize: v.FileSize,
			filename: nameOr(v.FileName, "video.mp4")}
	case msg.VideoNote != nil:
		v := msg.VideoNote
		return &attachment{fileID: v.FileID, fileUniqueID: v.FileUniqueID, fileSize: int64(v.FileSize),
			filename: "video_note.mp4", speech: true, duration: v.Duration}
	case msg.Animation != nil:
		a := msg.Animation
		return &attachment{fileID: a.FileID, fileUniqueID: a.FileUniqueID, fileSize: a.FileSize,
			filename: nameOr(a.FileName, "animation.mp4")}
	case msg.Sticker != nil:
		s := msg.Sticker
		return &attachment{fileID: s.FileID, fileUniqueID: s.FileUniqueID, fileSize: int64(s.FileSize),
			filename: "sticker.webp"}
	default:
		return nil
	}
}

func nameOr(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

// downloadAttachment fetches a file from Telegram and saves it into dir. Returns the local path.
func downloadAttachment(ctx context.Context, b *bot.Bot, att *attachment, dir string) (string, error) {
	localPath := filepath.Join(dir, localName(att))

	if isCached(localPath, att) {
		log.Printf("handler: file %s already cached, skipping download", localPath)
		return localPath, nil
	}

	f, err := b.GetFile(ctx, &bot.GetFileParams{FileID: att.fileID})
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}

	log.Printf("handler: downloading file %s (%s) to %s", att.filename, f.FilePath, localPath)

	if err := fetchToFile(ctx, b.FileDownloadLink(f), localPath); err != nil {
		return "", fmt.Errorf("download %s: %w", att.filename, err)
	}

	writeUID(localPath, att.fileUniqueID)

	return localPath, nil
}

// isCached returns true if the local file exists and matches the attachment's size and unique ID.
func isCached(localPath string, att *attachment) bool {
	info, err := os.Stat(localPath)
	if err != nil {
		return false
	}

	if att.fileSize > 0 && info.Size() != att.fileSize {
		return false
	}

	if att.fileUniqueID != "" {
		stored, _ := os.ReadFile(localPath + ".uid") //nolint:errcheck // missing sidecar file means cache miss
		if string(stored) != att.fileUniqueID {
			return false
		}
	}

	return true
}

func writeUID(localPath, uid string) {
	if uid == "" {
		return
	}
	if err := os.WriteFile(localPath+".uid", []byte(uid), 0o644); err != nil {
		log.Printf("handler: failed to write uid file: %v", err)
	}
}

func fetchToFile(ctx context.Context, link, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, http.NoBody)
	if err != nil {
		return errors.New("invalid download request")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return withoutURL(err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body already read

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return writeAtomically(dst, resp.Body)
}

func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

func writeAtomically(dst string, r io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.part")
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		removeFile(tmp.Name())
		return err
	}

	if err := os.Rename(tmp.Name(), dst); err != nil {
		removeFile(tmp.Name())
		return err
	}
	return nil
}

func removeFile(path string) {
	if err := os.Remove(path); err != nil {
		log.Printf("handler: remove %s: %v", path, err)
	}
}

func localName(att *attachment) string {
	base := filepath.Base(att.filename)
	if base == "." || base == ".." || base == string(filepath.Separator) {
		base = "file"
	}
	if att.fileUniqueID == "" {
		return base
	}
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext) + "_" + att.fileUniqueID + ext
}
