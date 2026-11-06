package ocr

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Сквозная проверка с настоящим Tesseract: титул рисуется шрифтом системы, распознаётся
// и раскладывается по полям. Нужны программа (FILIN_TESSERACT — путь к ней) и шрифт
// Windows; без них тест пропускается — это проверка установки, а не правил разбора.
func TestRecognizeDrawnTitle(t *testing.T) {
	bin := os.Getenv("FILIN_TESSERACT")
	if bin == "" {
		t.Skip("FILIN_TESSERACT не задан")
	}
	ttf, err := os.ReadFile(`C:\Windows\Fonts\georgia.ttf`)
	if err != nil {
		t.Skip("нет шрифта Georgia:", err)
	}
	f, err := opentype.Parse(ttf)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1500, 2300))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0xF2, 0xEA, 0xD8, 0xFF}), image.Point{}, draw.Src)
	put := func(s string, size float64, y int) {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			t.Fatal(err)
		}
		d := font.Drawer{Dst: img, Src: image.NewUniform(color.RGBA{0x2A, 0x22, 0x1A, 0xFF}), Face: face}
		w := d.MeasureString(s).Round()
		d.Dot = fixed.P((1500-w)/2, y)
		d.DrawString(s)
	}
	put("Е. В. ТАРЛЕ", 60, 380)
	put("НАШЕСТВИЕ", 130, 860)
	put("НАПОЛЕОНА", 130, 1010)
	put("НА РОССИЮ", 130, 1160)
	put("1812 год", 55, 1360)
	put("ИЗДАТЕЛЬСТВО «НАУКА»", 45, 1980)
	put("МОСКВА 1992", 45, 2060)
	var photo bytes.Buffer
	if err := jpeg.Encode(&photo, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tsv, err := Engine{Bin: bin}.Recognize(ctx, photo.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	d := Parse(ParseTSV(tsv))
	t.Logf("распознано: %q", d.Lines)
	want := Draft{Kind: KindTitle, People: []Person{}, ISBN: []string{}, Title: "Нашествие наполеона на россию", Subtitle: "1812 год", Authors: []string{"Е. В. Тарле"},
		Publisher: "Наука", City: "Москва", Year: 1992, Others: []string{}}
	d.Lines = nil
	// регистр мелкой строки Tesseract путает («1812 Год») — это распознавание, а не разбор
	d.Subtitle = strings.ToLower(d.Subtitle)
	if !reflect.DeepEqual(d, want) {
		t.Errorf("черновик\n got %+v\nwant %+v", d, want)
	}
}
