package cover

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// withOrientation вставляет после SOI сегмент APP1 с EXIF, где Orientation = o.
func withOrientation(t *testing.T, jpg []byte, o uint16) []byte {
	t.Helper()
	var tiff bytes.Buffer
	tiff.WriteString("II*\x00")
	binary.Write(&tiff, binary.LittleEndian, uint32(8)) // IFD0 сразу за заголовком
	binary.Write(&tiff, binary.LittleEndian, uint16(1)) // одна запись
	binary.Write(&tiff, binary.LittleEndian, []uint16{0x0112, 3})
	binary.Write(&tiff, binary.LittleEndian, uint32(1))
	binary.Write(&tiff, binary.LittleEndian, []uint16{o, 0})
	binary.Write(&tiff, binary.LittleEndian, uint32(0))
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write(jpg[:2])
	out.Write([]byte{0xFF, 0xE1})
	binary.Write(&out, binary.BigEndian, uint16(len(seg)+2))
	out.Write(seg)
	out.Write(jpg[2:])
	return out.Bytes()
}

func size(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

// Телефон снимает книгу «портретом», но пишет пиксели лёжа и тег 6 —
// без разворота обложка ляжет боком.
func TestPhoneOrientationIsApplied(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	if got := exifOrientation(withOrientation(t, buf.Bytes(), 6)); got != 6 {
		t.Fatalf("тег прочитан как %d", got)
	}
	out, err := Process(withOrientation(t, buf.Bytes(), 6))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, out); w != 20 || h != 40 {
		t.Fatalf("ждали 20×40 после разворота, получено %d×%d", w, h)
	}
}

func TestLargeIsScaledDown(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3000, 4000))); err != nil {
		t.Fatal(err)
	}
	out, err := Process(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, out); w != 900 || h != MaxSide {
		t.Fatalf("ждали 900×%d, получено %d×%d", MaxSide, w, h)
	}
}

func TestNotImage(t *testing.T) {
	if _, err := Process([]byte("%PDF-1.4 не картинка")); err != ErrNotImage {
		t.Fatalf("ждали ErrNotImage, получено %v", err)
	}
	if got := exifOrientation([]byte{0xFF, 0xD8, 0xFF}); got != 1 {
		t.Fatalf("обрезанный файл: %d", got)
	}
}

// Имя обложки из базы, пришедшей из чужого архива, не выводит за папку обложек.
func TestRemoveRefusesPaths(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "важный.txt")
	os.WriteFile(victim, []byte("x"), 0o644)
	d := Dir(filepath.Join(root, "covers"))
	os.Mkdir(string(d), 0o755)
	for _, name := range []string{`..\важный.txt`, "../важный.txt", victim} {
		d.Remove(name)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("удалён файл вне папки обложек: %v", err)
	}
}
