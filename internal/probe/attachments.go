package probe

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// picFormats maps cover art codecs to their file extension and MIME type
var picFormats = map[string]struct{ ext, mime string }{
	"mjpeg": {".jpg", "image/jpeg"},
	"png":   {".png", "image/png"},
	"bmp":   {".bmp", "image/bmp"},
	"gif":   {".gif", "image/gif"},
	"webp":  {".webp", "image/webp"},
}

// CanAttachPic reports whether a cover art stream can be stored as a Matroska attachment
func CanAttachPic(s Stream) bool {
	_, ok := picFormats[strings.ToLower(s.CodecName)]
	return ok
}

// AttachedPic is a cover art image extracted to a file, ready for -attach
type AttachedPic struct {
	Path     string
	Filename string // name stored in the MKV, e.g. cover.jpg
	MimeType string
}

// ExtractAttachedPics writes each cover art stream to an image file in dir.
//
// ffmpeg's Matroska muxer stores a stream-copied cover as a real 1-frame video
// track, which players treat as a second video. Cover art has to be extracted
// and re-added with -attach so it is stored as a proper MKV attachment.
func ExtractAttachedPics(inputPath string, pics []Stream, dir string) ([]AttachedPic, error) {
	var result []AttachedPic
	used := map[string]bool{}
	for i, s := range pics {
		format, ok := picFormats[strings.ToLower(s.CodecName)]
		if !ok {
			return nil, fmt.Errorf("cover art stream %d has unsupported codec %s", s.Index, s.CodecName)
		}

		// Keep the source's attachment name (e.g. MKV cover.jpg/small_cover.jpg); MP4 covers have none
		name := tagValue(s.Tags, "filename")
		if name == "" {
			name = "cover" + format.ext
		}
		base, ext := strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name)
		for n := 2; used[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s_%d%s", base, n, ext)
		}
		used[strings.ToLower(name)] = true

		mime := tagValue(s.Tags, "mimetype")
		if mime == "" {
			mime = format.mime
		}

		path := filepath.Join(dir, "pic_"+strconv.Itoa(i)+format.ext)
		cmd := exec.Command("ffmpeg", "-v", "error", "-i", inputPath,
			"-map", "0:"+strconv.Itoa(s.Index), "-c", "copy", "-frames:v", "1",
			"-f", "image2", "-update", "1", "-y", path)
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("failed to extract cover art stream %d: %w (output: %s)", s.Index, err, strings.TrimSpace(string(output)))
		}
		result = append(result, AttachedPic{Path: path, Filename: name, MimeType: mime})
	}
	return result, nil
}

// AttachArgs returns the ffmpeg arguments that attach pics to the output.
// firstAttachment is the number of attachment streams already mapped, since
// -attach streams are numbered after them.
func AttachArgs(pics []AttachedPic, firstAttachment int) []string {
	var args []string
	for i, pic := range pics {
		spec := fmt.Sprintf("-metadata:s:t:%d", firstAttachment+i)
		args = append(args,
			"-attach", pic.Path,
			spec, "mimetype="+pic.MimeType,
			spec, "filename="+pic.Filename,
		)
	}
	return args
}

// IsMatroska reports whether a path's extension is a Matroska container
func IsMatroska(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mka", ".mk3d":
		return true
	}
	return false
}

// tagValue returns a tag case-insensitively
func tagValue(tags map[string]string, key string) string {
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}
