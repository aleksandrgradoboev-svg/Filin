package web

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	gzqr "github.com/makiuchi-d/gozxing/qrcode"
)

// QR читается обратно независимым распознавателем (ZXing) — в картинке ровно тот текст,
// который снимет телефон.
func TestQRRoundTrip(t *testing.T) {
	for _, text := range []string{
		"http://192.168.1.10:8080/app.apk",
		pairText("http://192.168.1.10:8080", "724048"),
	} {
		src := string(qrImage(text))
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(src, "data:image/png;base64,"))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		bmp, err := gozxing.NewBinaryBitmapFromImage(img)
		if err != nil {
			t.Fatal(err)
		}
		res, err := gzqr.NewQRCodeReader().Decode(bmp, nil)
		if err != nil {
			t.Fatalf("%q не прочитался: %v", text, err)
		}
		if res.GetText() != text {
			t.Errorf("в QR %q, ждали %q", res.GetText(), text)
		}
	}
}
