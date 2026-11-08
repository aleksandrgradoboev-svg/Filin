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
	det, rec, ori *session
	chars         []string
}

// Models — файлы моделей: поиск строк, чтение строк, словарь знаков чтения (по строке на
// знак) и ориентация страницы (пусто — снимок считается стоящим прямо).
type Models struct {
	Det, Rec []byte
	Dict     []byte
	Ori      []byte
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
	var ori *session
	if len(m.Ori) > 0 {
		if ori, err = rt.newSession(m.Ori, "x", "fetch_name_0", threads); err != nil {
			det.close()
			rec.close()
			return nil, fmt.Errorf("модель ориентации: %w", err)
		}
	}
	chars := strings.Split(strings.TrimRight(strings.ReplaceAll(string(m.Dict), "\r\n", "\n"), "\n"), "\n")
	return &Engine{det: det, rec: rec, ori: ori, chars: chars}, nil
}

// Close освобождает модели (сама библиотека остаётся загруженной до конца процесса).
func (e *Engine) Close() {
	e.det.close()
	e.rec.close()
	if e.ori != nil {
		e.ori.close()
	}
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

// Recognize — таблица слов снимка в формате tesseract tsv: снимок выпрямлен по модели
// ориентации, координаты — в выпрямленном снимке исходного масштаба.
func (e *Engine) Recognize(img image.Image) ([]byte, error) {
	w, h, lines, err := e.Lines(img)
	if err != nil {
		return nil, err
	}
	return TSV(w, h, lines), nil
}

// Lines — строки снимка сверху вниз и размер выпрямленного снимка в исходном масштабе.
func (e *Engine) Lines(img image.Image) (int, int, []Line, error) {
	b := img.Bounds()
	src := prepare(img)
	scale := float64(b.Dx()) / float64(src.Rect.Dx())
	if e.ori != nil {
		q, err := e.orientation(src)
		if err != nil {
			return 0, 0, nil, err
		}
		src = rotateCW(src, 4-q) // повёрнута на q по часовой — вернуть против
	}
	pw, ph := int(float64(src.Rect.Dx())*scale+0.5), int(float64(src.Rect.Dy())*scale+0.5)
	boxes, err := e.detect(src)
	if err != nil {
		return 0, 0, nil, err
	}
	var out []Line
	for _, r := range boxes {
		text, conf, err := e.read(crop(src, r))
		if err != nil {
			return 0, 0, nil, err
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
	return pw, ph, out, nil
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
	// буквы, у которых есть русский двойник: слово только из них может быть русским
	reTwinsOnly = regexp.MustCompile(`^[ABCEHKMOPTXaceopxy]+$`)
	// служебные слова карточки, которые модель читает латиницей: «BBK 63.3(0)62», «YDK 94:355»
	reBBK = regexp.MustCompile(`^(?:BBK|BEK|ВВК|ВЕК|BБК)$`)
	reUDK = regexp.MustCompile(`^(?:YDK|УDK|YДК|УДK)$`)
	// авторский знак: одна буква и номер — «X 24»
	reSign = regexp.MustCompile(`^[ABCEHKMOPTX]\s?\d{1,3}$`)
)

// cyrillic — модель читает и латиницу, и кириллицу и путает похожие буквы («MOCKBA»,
// «BBK», «E. B. Tаpле»). Слово с русской буквой — русское целиком; слово из одних
// «двойников» — русское, если в строке русских букв не меньше латинских.
func cyrillic(s string) string {
	if reSign.MatchString(s) {
		return latToCyr.Replace(s)
	}
	// служебные слова — до двойников: «BBK» из одних двойников стало бы «ВВК», а не «ББК»
	ws := strings.Fields(s)
	for i, w := range ws {
		core := strings.Trim(w, ".,:;!?«»\"()[]-—–/")
		if reBBK.MatchString(core) {
			ws[i] = strings.Replace(w, core, "ББК", 1)
		} else if reUDK.MatchString(core) {
			ws[i] = strings.Replace(w, core, "УДК", 1)
		}
	}
	s = strings.Join(ws, " ")
	c, l := len(reCyr.FindAllString(s, -1)), len(reLat.FindAllString(s, -1))
	// все латинские буквы строки — двойники («MOCKBA-1958»): английское слово из одних
	// A, B, C, E, H, K, M, O, P, T, X на советском титуле не встречается
	if l > 0 && reTwinsOnly.MatchString(strings.Join(reLat.FindAllString(s, -1), "")) {
		return latToCyr.Replace(s)
	}
	ruLine := c > 0 && c >= l
	words := strings.Fields(s)
	for i, w := range words {
		core := strings.Trim(w, ".,:;!?«»\"()[]-—–/")
		switch {
		case reBBK.MatchString(core):
			words[i] = strings.Replace(w, core, "ББК", 1)
		case reUDK.MatchString(core):
			words[i] = strings.Replace(w, core, "УДК", 1)
		case reCyr.MatchString(w):
			words[i] = latToCyr.Replace(w)
		case ruLine && reTwinsOnly.MatchString(core):
			words[i] = latToCyr.Replace(w)
		case c > 0 && reInitial.MatchString(w): // инициал рядом с русской фамилией: «E. B. Тарле»
			words[i] = latToCyr.Replace(w)
		}
	}
	return strings.Join(words, " ")
}

var reInitial = regexp.MustCompile(`^[ABCEHKMOPTX]\.,?$`)
