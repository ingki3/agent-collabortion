package artifacts

import (
	"bytes"
	"testing"
)

// DetectContentType is PRD FR-4.3.1 rule 1. 회귀 주입: text/plain·text/xml
// 분기의 `.svg` 규칙을 지우면 (svg) FAIL; octet-stream 분기의 확장자 표를 지우면
// (mp3·m4a) FAIL; 서명보다 확장자를 먼저 보면 (spoof) FAIL.
func TestDetectContentType(t *testing.T) {
	png := []byte("\x89PNG\x0D\x0A\x1A\x0A\x00\x00")
	html := []byte("<!DOCTYPE html><html><script>x</script>")
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	svgBare := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)
	mp3ID3 := []byte("ID3\x03\x00\x00\x00\x00\x00\x00rest")
	mp3Frame := append([]byte{0xFF, 0xFB, 0x90, 0x64}, bytes.Repeat([]byte{0x11}, 40)...)
	m4a := append([]byte("\x00\x00\x00\x20ftypM4A \x00\x00\x00\x00M4A mp42isom"), bytes.Repeat([]byte{0}, 16)...)
	mp4 := append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), bytes.Repeat([]byte{0}, 16)...)
	webm := []byte("\x1A\x45\xDF\xA3\x01\x00\x00")
	ogg := []byte("OggS\x00\x02\x00\x00")
	wav := []byte("RIFF\x24\x00\x00\x00WAVEfmt ")
	for _, c := range []struct {
		tag, name string
		data      []byte
		want      string
	}{
		{"png", "a.png", png, "image/png"},
		{"png-noext", "a", png, "image/png"},
		{"spoof", "evil.png", html, "text/html; charset=utf-8"},
		{"spoof-svg-html", "evil.svg", html, "text/html; charset=utf-8"},
		{"svg", "logo.svg", svg, "image/svg+xml"},
		{"svg", "logo.SVG", svgBare, "image/svg+xml"},
		{"svg-wrong-name", "logo.txt", svg, "text/xml; charset=utf-8"},
		{"mp3", "bgm.mp3", mp3ID3, "audio/mpeg"},
		{"mp3", "bgm.mp3", mp3Frame, "audio/mpeg"},
		{"m4a", "voice.m4a", m4a, "audio/mp4"},
		{"mp4", "play.mp4", mp4, "video/mp4"},
		{"webm", "clip.webm", webm, "video/webm"},
		{"weba", "clip.weba", webm, "audio/webm"},
		{"ogg", "a.ogg", ogg, "audio/ogg"},
		{"wav", "a.wav", wav, "audio/wav"},
		{"text-as-mp3", "notes.mp3", []byte("hello"), "text/plain; charset=utf-8"},
		{"bin", "blob.bin", []byte{0, 1, 2, 3}, "application/octet-stream"},
		{"md", "r.md", []byte("# hi"), "text/markdown; charset=utf-8"},
	} {
		if got := DetectContentType(c.name, c.data); got != c.want {
			t.Errorf("(%s) %s → %q, want %q", c.tag, c.name, got, c.want)
		}
	}
}

func TestPreviewable(t *testing.T) {
	for ct, want := range map[string]bool{
		"image/png": true, "image/svg+xml": true, "video/mp4": true, "audio/mpeg": true, "audio/mp4": true,
		"text/html; charset=utf-8": false, "text/xml; charset=utf-8": false, "application/octet-stream": false,
		"image/bmp": false, "": false, "IMAGE/PNG": true,
	} {
		if got := Previewable(ct); got != want {
			t.Errorf("Previewable(%q) = %v, want %v", ct, got, want)
		}
	}
}
