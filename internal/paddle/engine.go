// Package paddle — распознавание текста моделями PaddleOCR (PP-OCR) через ONNX Runtime,
// без Python и без Tesseract: поиск строк, чтение каждой строки, ответ — таблица в формате
// вывода tesseract tsv, чтобы её разбирал тот же ocr.ParseTSV.
package paddle

import (
	"bytes"
	"fmt"
	"image"
	"regexp"
	"strings"
)

// Engine — загруженные модели. Безопасен для одновременных вызовов: каждая модель
// прогоняется по очереди.
type Engine struct {
	det, rec *session
	chars    []string
}

// Models — файлы моделей: поиск строк, чтение строк и словарь знаков чтения (по строке на знак).
type Models struct {
	Det, Rec []byte
	Dict     []byte
}

// Open загружает ONNX Runtime из dll и модели. threads — потоков на прогон (0 — сколько решит
// ONNX Runtime).
func Open(dll string, m Models, threads int) (*Engine, error) {
	rt, err := loadORT(dll)
	if err != nil {
		return nil, err
	}
	det, err := rt.newSession(m.Det, "x", "fetch_name_0", threads)
	if err != nil {
		return nil, fmt.Errorf("модель поиска строк: %w", err)
	}
	rec, err := rt.newSession(m.Rec, "x", "fetch_name_0", threads)
	if err != nil {
		det.close()
		return nil, fmt.Errorf("модель чтения: %w", err)
	}
	chars := strings.Split(strings.TrimRight(strings.ReplaceAll(string(m.Dict), "\r\n", "\n"), "\n"), "\n")
	return &Engine{det: det, rec: rec, chars: chars}, nil
}

// Close освобождает модели (сама библиотека остаётся загруженной до конца процесса).
func (e *Engine) Close() {
	e.det.close()
	e.rec.close()
}

// Line — прочитанная строка: текст, рамка в координатах исходного снимка, уверенность 0–1.
type Line struct {
	Text   string
	Left   int
	Top    int
	Width  int
	Height int
	Conf   float64
}

// Lines — строки снимка сверху вниз.
func (e *Engine) Lines(img image.Image) ([]Line, error) {
	b := img.Bounds()
	src := prepare(img)
	scale := float64(b.Dx()) / float64(src.Rect.Dx())
	boxes, err := e.detect(src)
	if err != nil {
		return nil, err
	}
	var out []Line
	for _, r := range boxes {
		text, conf, err := e.read(crop(src, r))
		if err != nil {
			return nil, err
		}
		text = strings.Join(strings.Fields(text), " ")
		if text == "" || conf < recMinScore {
			continue
		}
		xmin, ymin, xmax, ymax := bounds(r)
		out = append(out, Line{
			Text: cyrillic(text), Conf: conf,
			Left: int(xmin * scale), Top: int(ymin * scale),
			Width: int((xmax - xmin) * scale), Height: int((ymax - ymin) * scale),
		})
	}
	return out, nil
}

func bounds(r rect) (xmin, ymin, xmax, ymax float64) {
	c := r.corners()
	xmin, ymin, xmax, ymax = c[0].X, c[0].Y, c[0].X, c[0].Y
	for _, p := range c[1:] {
		xmin, xmax = min(xmin, p.X), max(xmax, p.X)
		ymin, ymax = min(ymin, p.Y), max(ymax, p.Y)
	}
	return max(xmin, 0), max(ymin, 0), xmax, ymax
}

// TSV — строки в формате `tesseract … tsv`: страница (уровень 1) и по строке на «слово»
// уровня 5, каждая в своём блоке — ocr.ParseTSV склеит соседние куски сам.
func TSV(width, height int, lines []Line) []byte {
	var b bytes.Buffer
	b.WriteString("level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n")
	fmt.Fprintf(&b, "1\t1\t0\t0\t0\t0\t0\t0\t%d\t%d\t-1\t\n", width, height)
	for i, l := range lines {
		fmt.Fprintf(&b, "5\t1\t%d\t1\t1\t1\t%d\t%d\t%d\t%d\t%.1f\t%s\n", i+1, l.Left, l.Top, l.Width, l.Height, l.Conf*100, l.Text)
	}
	return b.Bytes()
}

var (
	reCyr = regexp.MustCompile(`[А-Яа-яЁё]`)
	reLat = regexp.MustCompile(`[A-Za-z]`)
	// латинские буквы, неотличимые от русских на титуле
	latToCyr = strings.NewReplacer(
		"A", "А", "B", "В", "C", "С", "E", "Е", "H", "Н", "K", "К", "M", "М", "O", "О", "P", "Р",
		"T", "Т", "X", "Х", "a", "а", "c", "с", "e", "е", "o", "о", "p", "р", "x", "х", "y", "у")
)

// cyrillic — модель читает и латиницу, и кириллицу и путает похожие буквы («MOCKBA»,
// «BBK»): в строке, где русских букв не меньше латинских, похожие латинские — русские.
func cyrillic(s string) string {
	c, l := len(reCyr.FindAllString(s, -1)), len(reLat.FindAllString(s, -1))
	if c > 0 && c >= l {
		return latToCyr.Replace(s)
	}
	return s
}
