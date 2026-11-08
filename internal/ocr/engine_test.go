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
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Вывод определителя ориентации: верим только уверенному, поворот — в четвертях по часовой.
func TestParseOSD(t *testing.T) {
	osd := func(rot, conf string) string {
		return "Page number: 0\r\nOrientation in degrees: 0\r\nRotate: " + rot + "\r\nOrientation confidence: " + conf + "\r\nScript: Cyrillic\r\n"
	}
	for _, c := range []struct {
		out  string
		want int
	}{
		{osd("180", "6.40"), 2}, {osd("90", "9.45"), 1}, {osd("270", "3.1"), 3}, {osd("0", "12.8"), 0},
		{osd("180", "0.23"), 0}, // не уверен — так он «видел арабское письмо» на мелком шрифте в тени
		{"", 0}, {"Too few characters. Skipping this page", 0},
	} {
		if got := parseOSD(c.out); got != c.want {
			t.Errorf("parseOSD(%q) = %d, ждали %d", c.out, got, c.want)
		}
	}
}

func TestRotate(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 3, 2)) // 3×2: левый верхний пиксель отмечен
	g.SetGray(0, 0, color.Gray{Y: 255})
	for _, c := range []struct {
		q    int
		w, h int
		x, y int // куда ушёл отмеченный пиксель
	}{{0, 3, 2, 0, 0}, {1, 2, 3, 1, 0}, {2, 3, 2, 2, 1}, {3, 2, 3, 0, 2}} {
		r := rotate(g, c.q)
		if r.Bounds().Dx() != c.w || r.Bounds().Dy() != c.h || r.GrayAt(c.x, c.y).Y != 255 {
			t.Errorf("поворот на %d четвертей: %v, отметка в (%d,%d) = %d", c.q, r.Bounds(), c.x, c.y, r.GrayAt(c.x, c.y).Y)
		}
	}
}

// Оценка прохода: уверенно прочитанные знаки, а годный ISBN в любой строке перевешивает
// пару лишних слов; мусор с низкой уверенностью не считается.
func TestScore(t *testing.T) {
	row := func(line int, text string, conf int) string {
		var b strings.Builder
		for i, w := range strings.Fields(text) {
			b.WriteString("5\t1\t1\t1\t" + strconv.Itoa(line) + "\t" + strconv.Itoa(i+1) + "\t" + strconv.Itoa(100*i) + "\t" +
				strconv.Itoa(100*line) + "\t90\t40\t" + strconv.Itoa(conf) + "\t" + w + "\n")
		}
		return b.String()
	}
	head := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
	plain := []byte(head + row(1, "Степкин Онтарио", 90) + row(2, "мусор", 20))
	if got := score(plain); got != len([]rune("СтепкинОнтарио")) {
		t.Errorf("без ISBN: %d", got)
	}
	withISBN := []byte(head + row(1, "15Вжх 978-5-600-01715-3 удк 94:355:359", 70))
	badISBN := []byte(head + row(1, "15Вжх 978-5-600-01715-4 удк 94:355:359", 70))
	if score(withISBN)-score(badISBN) != isbnWeight {
		t.Errorf("годный ISBN внутри строки не засчитан: %d против %d", score(withISBN), score(badISBN))
	}
	// 10 цифр засчитываются только с подписью «ISBN»: без неё это скорее индекс издания
	labeled := []byte(head + row(1, "ISBN 5-250-02008-9", 70))
	bare := []byte(head + row(1, "5-250-02008-9", 70))
	if got := score(labeled) - score(bare); got != isbnWeight+len("ISBN") {
		t.Errorf("ISBN-10: с подписью %d, без %d", score(labeled), score(bare))
	}
}

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
