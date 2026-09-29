package transport_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// uploadImage posts a file to /uploads under a chosen filename and returns the
// response.
func uploadImage(t *testing.T, token, filename, contentType string, data []byte) (*http.Response, any) {
	t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("image", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, testServer.URL+"/uploads", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// A bearer token is exempt from the CSRF check by design, so this helper does
	// not need a CSRF token or a cookie jar.
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /uploads: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp, any(out)
}

// getRaw fetches a URL without touching the body, for the binary responses the
// JSON helper cannot decode.
func getRaw(t *testing.T, path string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// uploadURL pulls the stored path out of an upload response.
func uploadURL(t *testing.T, out any) string {
	t.Helper()

	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("upload response is %T, want an object", out)
	}
	url, _ := m["url"].(string)
	if url == "" {
		t.Fatalf("upload response has no url: %v", m)
	}
	return url
}

// realJPEG returns bytes that are genuinely a JPEG, so the test does not depend
// on the handler trusting an extension.
func realJPEG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func realPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(x * 20), B: uint8(y * 20), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// xssSVG is the payload the old handler stored happily: an SVG whose script runs
// on the origin that serves it.
const xssSVG = `<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">
  <script>alert(document.cookie)</script>
  <rect width="100" height="100" fill="red"/>
</svg>`

// TestUploadRejectsSVG is #17 inverted.
//
// The old handler validated nothing. It wrote the client's bytes under the
// client's filename and served them with the content-type implied by the
// extension, so an .svg landed on our origin as image/svg+xml: opening the
// review image executed the script inside it with access to the DOM, the
// cookies and localStorage of whoever looked at it. Stored XSS, and the
// browser's <img> tag is no defence because the payload was reached by
// navigating to the file directly.
func TestUploadRejectsSVG(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	for _, name := range []string{"evil.svg", "evil.SVG", "evil.svgz", "evil.png.svg"} {
		resp, out := uploadImage(t, token, name, "image/svg+xml", []byte(xssSVG))
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%s: status %d, want 415 (body=%v)", name, resp.StatusCode, out)
		}
	}
}

// TestUploadsAcceptRealImages is the other half: a legitimate photo must still
// work, and it must come back as something a browser will render.
func TestUploadsAcceptRealImages(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	cases := []struct {
		name      string
		filename  string
		data      func(*testing.T) []byte
		wantType  string
		wantBytes bool
	}{
		{"png", "photo.png", realPNG, "image/png", true},
		{"jpg", "photo.jpg", realJPEG, "image/jpeg", true},
		// A PNG named .jpg: the encoder follows the name the caller used, but
		// the bytes must still be a real image. This is not a sniff test, it is
		// a decode test.
		{"png_named_jpg", "photo.jpg", realPNG, "image/jpeg", true},
	}

	for _, tc := range cases {
		resp, out := uploadImage(t, token, tc.filename, "application/octet-stream", tc.data(t))
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("%s: status %d, want 201 (body=%v)", tc.name, resp.StatusCode, out)
			continue
		}

		url := uploadURL(t, out)
		if !strings.HasPrefix(url, "/uploads/") {
			t.Errorf("%s: url = %q, want a path under /uploads/", tc.name, url)
			continue
		}
		// The server picks the name. The old handler used the client's, which is
		// how "evil.png.svg" and "../" both survived.
		if base := url[strings.LastIndex(url, "/")+1:]; strings.Contains(base, "evil") || strings.Contains(base, "photo") {
			t.Errorf("%s: stored name %q is derived from the client's filename", tc.name, base)
		}

		served := getRaw(t, url)
		if served.StatusCode != http.StatusOK {
			t.Errorf("%s: GET %s: status %d, want 200", tc.name, url, served.StatusCode)
			continue
		}
		if got := served.Header.Get("Content-Type"); got != tc.wantType {
			t.Errorf("%s: Content-Type = %q, want %q", tc.name, got, tc.wantType)
		}
		if got := served.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", tc.name, got)
		}
		if got := served.Header.Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
			t.Errorf("%s: Content-Security-Policy = %q, want a sandbox", tc.name, got)
		}
		if got := served.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
			t.Errorf("%s: Content-Disposition = %q, want inline", tc.name, got)
		}
		if tc.wantBytes {
			raw, _ := io.ReadAll(served.Body)
			if len(raw) == 0 {
				t.Errorf("%s: served an empty body", tc.name)
			}
			if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
				t.Errorf("%s: the served bytes are not a decodable image: %v", tc.name, err)
			}
		}
	}
}

// TestUploadRejectsNonImages is the allowlist doing its job against a file whose
// extension is right and whose contents are not.
func TestUploadRejectsNonImages(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	cases := []struct {
		name string
		file string
		data []byte
	}{
		{"script_as_png", "shell.png", []byte("<?php system($_GET['c']); ?>")},
		{"html_as_jpg", "page.jpg", []byte("<html><script>alert(1)</script></html>")},
		{"empty", "empty.png", nil},
		{"truncated_png", "half.png", realPNG(t)[:20]},
		{"elf_binary", "tool.jpg", []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}},
	}

	for _, tc := range cases {
		resp, _ := uploadImage(t, token, tc.file, "image/png", tc.data)
		if resp.StatusCode == http.StatusCreated {
			t.Errorf("%s: status 201, want a rejection", tc.name)
		}
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%s: status %d, want 400 or 415", tc.name, resp.StatusCode)
		}
	}
}

// jpegWithComment splices a COM (comment) segment carrying payload into a real
// JPEG, right after the SOI marker.
//
// A comment is a legitimate part of the format, decoders skip it, and it never
// reaches the pixels. That is exactly why it is the right thing to test: nothing
// in "does it decode?" would catch it, and a browser rendering the result in an
// <img> ignores it too. It survives a naive copy-to-disk, so the only thing that
// removes it is re-encoding.
func jpegWithComment(t *testing.T, payload string) []byte {
	t.Helper()

	src := realJPEG(t)
	if len(src) < 2 || src[0] != 0xFF || src[1] != 0xD8 {
		t.Fatalf("the encoder did not produce a JPEG (starts with % x)", src[:2])
	}

	seg := make([]byte, 0, len(payload)+4)
	seg = append(seg, 0xFF, 0xFE) // COM
	n := len(payload) + 2
	seg = append(seg, byte(n>>8), byte(n)) // big-endian length, inclusive
	seg = append(seg, payload...)

	out := make([]byte, 0, len(src)+len(seg))
	out = append(out, src[:2]...) // SOI
	out = append(out, seg...)
	out = append(out, src[2:]...)
	return out
}

// TestUploadStripsEmbeddedPayloads is the reason the handler re-encodes instead
// of validating.
//
// Checking that a file decodes proves it is a picture. It does not prove the
// stored bytes are only pixels, and the difference is where a decoder bug
// becomes a stored XSS: a comment, a text chunk, a palette entry with a payload
// in it. Re-encoding is what makes the stored file a function of the decoded
// image and nothing else.
func TestUploadStripsEmbeddedPayloads(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	payload := `<script>alert(document.cookie)</script>`
	smuggled := jpegWithComment(t, payload)
	if !bytes.Contains(smuggled, []byte(payload)) {
		t.Fatal("the test fixture does not contain the payload")
	}

	resp, out := uploadImage(t, token, "sneaky.jpg", "image/jpeg", smuggled)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201 (body=%v)", resp.StatusCode, out)
	}

	served := getRaw(t, uploadURL(t, out))
	raw, _ := io.ReadAll(served.Body)

	if bytes.Contains(raw, []byte(payload)) {
		t.Fatal("the stored bytes still contain the injected comment; the file was copied, not re-encoded")
	}
	if !bytes.HasPrefix(raw, []byte{0xFF, 0xD8}) {
		t.Fatal("the stored bytes are not a JPEG")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
		t.Fatalf("the stored bytes do not decode as an image: %v", err)
	}
	// The pixels came through: a re-encode that threw the image away would be no
	// better than one that kept the payload.
	if _, err := jpeg.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("the stored bytes do not decode as a JPEG: %v", err)
	}
}

// TestUploadIgnoresClientContentType proves the header the browser sent is not
// part of the decision. Everything the client says is a claim; only the bytes
// and the decode are evidence.
func TestUploadIgnoresClientContentType(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	resp, out := uploadImage(t, token, "x.png", "text/html; charset=utf-8", realPNG(t))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201 (body=%v)", resp.StatusCode, out)
	}
	served := getRaw(t, uploadURL(t, out))
	if got := served.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
}

// TestUploadRefusesTraversal covers the name half of the bug: the old handler
// ran the client's filename through a filter that allowed dots, so a name could
// escape the directory or arrive with an extension the server then trusted.
func TestUploadRefusesTraversal(t *testing.T) {
	for _, name := range []string{
		"../../etc/passwd.png",
		"..%2f..%2fetc.png",
		"a/b.png",
		"....//....//x.png",
	} {
		req, _ := http.NewRequest(http.MethodGet, testServer.URL+"/uploads/"+name, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s: status 200, want a rejection", name)
		}
		if bytes.Contains(body, []byte("root:")) {
			t.Errorf("%s: the response contains /etc/passwd contents", name)
		}
	}
}

// TestUploadRequiresASession keeps uploads behind Auth even though the router
// already does it: the limit and the auth check are the two things standing
// between the disk and an anonymous caller.
func TestUploadRequiresASession(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("image", "x.png")
	part.Write(realPNG(t))
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, testServer.URL+"/uploads", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /uploads: %v", err)
	}
	defer resp.Body.Close()
	// CSRF sits outside Auth, so an anonymous multipart POST is refused before
	// the session check is ever reached. Either refusal is correct; what matters
	// is that it is not 201.
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous upload: status %d, want 401 or 403", resp.StatusCode)
	}
}

// realGIF is a single-frame GIF, which is what a re-encoded upload becomes.
func realGIF(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 40, G: uint8(x * 25), B: uint8(y * 25), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

// TestWebPIsRefused pins the removal of the one format the server could not take
// apart.
//
// The allowlist used to contain webp with a nil encoder, and nil meant "store
// the client's bytes". The check in front of it was two magic bytes and a nosniff
// header, so the file the browser went on to decode had never been parsed by the
// process that stored it. image/ has no WebP decoder, and the fix is not a
// better check: the format is not accepted at all.
//
// JPEG, PNG and GIF each have a real re-encoder, so every byte of every stored
// upload is a byte this process produced.
func TestWebPIsRefused(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	// A well-formed WebP container header, which is all the old check ever
	// looked at. It does not need real image data: the request has to be refused
	// on the extension, before anything tries to decode it.
	webp := []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")

	resp, out := uploadImage(t, token, "photo.webp", "image/webp", webp)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("webp upload: status %d, want 415 (body=%v)", resp.StatusCode, out)
	}
}

// TestEveryStoredImageWasReEncodedByThisProcess is the property the allowlist
// exists to guarantee, checked for every accepted format rather than one at a
// time.
//
// The payload is a valid image with a script tag glued to the end. It survives
// any magic-byte check, because the magic bytes are still at the front, and it
// is exactly what a stored-XSS attempt looks like. Re-encoding is what destroys
// it, so each format is uploaded with the same tail and the served bytes are
// compared against it.
func TestEveryStoredImageWasReEncodedByThisProcess(t *testing.T) {
	payload := []byte("<<script>alert(document.domain)</script>>")

	for _, tc := range []struct {
		name        string
		filename    string
		build       func(*testing.T) []byte
		contentType string
	}{
		{"png", "shot.png", realPNG, "image/png"},
		{"jpeg", "shot.jpg", realJPEG, "image/jpeg"},
		{"gif", "shot.gif", realGIF, "image/gif"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, token := setupUsersAndToken(t)

			original := append(tc.build(t), payload...)
			resp, out := uploadImage(t, token, tc.filename, tc.contentType, original)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("upload with a trailing payload: status %d, want 201 (body=%v)", resp.StatusCode, out)
			}
			url := uploadURL(t, out)

			served, err := io.ReadAll(getRaw(t, url).Body)
			if err != nil {
				t.Fatalf("read served file: %v", err)
			}
			if bytes.Contains(served, payload) {
				t.Errorf("the served %s still contains the bytes the client appended: it was stored, not re-encoded", tc.name)
			}
			if got := http.DetectContentType(served); !strings.HasPrefix(got, tc.contentType) {
				t.Errorf("served %s detects as %q, want %s", tc.name, got, tc.contentType)
			}
		})
	}
}
