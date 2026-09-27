package artifacts

import (
	"net/http"
	"path/filepath"
	"strings"
)

// DetectContentType is PRD FR-4.3.1 rule 1 (openapi v0.3.7
// Artifact.content_type): the server decides what an artifact IS from its
// first bytes and its name. What the uploader declared (the multipart part's
// Content-Type — the CLI sends application/octet-stream for anything it does
// not recognise, a browser sends whatever the OS guessed, and a hostile client
// sends image/png on an HTML page) is never read.
//
// The order is: the content's signature first (http.DetectContentType, the
// WHATWG sniffing table), then the file name only where the signature cannot
// speak:
//
//   - A recognised signature wins over the name. `x.png` whose bytes are an
//     HTML page is text/html — never a preview type.
//   - SVG has no binary signature: it sniffs as text/xml or text/plain, and
//     only then does `.svg` make it image/svg+xml. An SVG that sniffs as
//     text/html (starts with <html>, <script>, …) stays text/html.
//   - Audio and video containers whose brand Go's table does not know (M4A,
//     mp4 with an `isom` brand, MPEG audio without an ID3 tag) sniff as
//     application/octet-stream; only then does a media extension decide.
//     Text never becomes media by its name.
//   - Images have exact signatures in the table, so an image extension on
//     bytes that do not match one decides nothing.
func DetectContentType(name string, data []byte) string {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	sniffed := http.DetectContentType(head)
	ext := strings.ToLower(filepath.Ext(name))
	base, _, _ := strings.Cut(sniffed, ";")
	base = strings.TrimSpace(base)

	switch base {
	case "text/xml", "text/plain":
		if ext == ".svg" {
			return "image/svg+xml"
		}
		if base == "text/plain" {
			if t, ok := textByExt[ext]; ok {
				return t
			}
		}
		return sniffed
	case "application/octet-stream":
		if t, ok := mediaByExt[ext]; ok {
			return t
		}
		if t, ok := binaryByExt[ext]; ok {
			return t
		}
		return sniffed
	case "audio/wave":
		// The sniffing table's name; browsers and the preview list say wav.
		return "audio/wav"
	case "application/ogg":
		if ext == ".ogv" {
			return "video/ogg"
		}
		return "audio/ogg"
	case "video/webm":
		// One EBML signature for both; the name says which player.
		if ext == ".weba" {
			return "audio/webm"
		}
		return base
	case "video/mp4":
		if ext == ".m4a" {
			return "audio/mp4"
		}
		return base
	}
	return sniffed
}

// mediaByExt is consulted only when the bytes had no signature Go knows
// (application/octet-stream) — see DetectContentType.
var mediaByExt = map[string]string{
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".weba": "audio/webm",
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
}

// binaryByExt names common non-preview binaries so the download carries a
// useful type; none of them is in the preview list.
var binaryByExt = map[string]string{
	".zip": "application/zip",
	".pdf": "application/pdf",
}

// textByExt refines text/plain by name. None of these is a preview type and
// none is HTML: an `.html` file that sniffs as plain text stays text/plain.
var textByExt = map[string]string{
	".md":   "text/markdown; charset=utf-8",
	".json": "application/json",
	".csv":  "text/csv; charset=utf-8",
}

// previewTypes is openapi v0.3.7 downloadArtifact `?inline=true`'s list: the
// only content types the server will hand out as `Content-Disposition:
// inline`. HTML is deliberately absent (FR-4.3.1 rule 3).
var previewTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/svg+xml": true,
	"video/mp4": true, "video/webm": true,
	"audio/mpeg": true, "audio/wav": true, "audio/ogg": true, "audio/webm": true, "audio/mp4": true,
}

// Previewable reports whether a judged content type may be served inline.
func Previewable(contentType string) bool {
	base, _, _ := strings.Cut(contentType, ";")
	return previewTypes[strings.ToLower(strings.TrimSpace(base))]
}
