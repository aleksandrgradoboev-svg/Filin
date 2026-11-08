package ocr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
)

// ErrNoEngine — Tesseract не найден: распознавание не установлено на этом компьютере.
var ErrNoEngine = errors.New("распознавание не установлено: нет программы Tesseract на компьютере с Филином")

// side — длинная сторона картинки для распознавания. Фото с телефона — 4000 пикселей и
// больше: Tesseract на них медленнее, а точнее не становится; кегль титула крупный.
const side = 2400

// Engine — вызов программы tesseract. Bin — путь к ней или имя в PATH.
type Engine struct {
	Bin string
}

// Available — найдена ли программа.
func (e Engine) Available() bool {
	_, err := exec.LookPath(e.bin())
	return err == nil
}

func (e Engine) bin() string {
	if e.Bin == "" {
		return "tesseract"
	}
	return e.Bin
}

// Recognize распознаёт фото титула и возвращает таблицу слов (вывод tsv) — её
// разбирает ParseTSV, а сохраняется она как есть, чтобы разбор можно было переиграть.
func (e Engine) Recognize(ctx context.Context, photo []byte) ([]byte, error) {
	if !e.Available() {
		return nil, ErrNoEngine
	}
	img, err := cover.Decode(photo)
	if err != nil {
		return nil, err
	}
	// Снимок с телефона — не скан: страница бывает вверх ногами или боком, в тени от руки,
	// с полкой в кадре. Сначала — выпрямить по определителю ориентации и распознать как
	// обычно. Текста почти нет — запасные проходы, и из всех берётся тот, где больше
	// уверенно прочитанного (замер на настоящих снимках — corpus_test.go).
	full := gray(cover.Fit(img, side))
	q := e.orientation(ctx, full)
	big := rotate(full, q)
	small := rotate(gray(cover.Fit(img, smallSide)), q)
	sauvola := []string{"-c", "thresholding_method=2"}
	best, err := e.run(ctx, big)
	if err != nil {
		return nil, err
	}
	bestScore := score(best)
	pick := func(g image.Image, extra ...string) {
		if ctx.Err() != nil {
			return
		}
		if out, err := e.run(ctx, g, extra...); err == nil {
			if s := score(out); s > bestScore {
				best, bestScore = out, s
			}
		}
	}
	// Местный порог (Sauvola) на уменьшенном снимке: тень от руки и тёмный край кадра
	// сбивают общий порог, и серый шрифт уходит в белое; крупный тонкий шрифт титула на
	// меньшем масштабе читается лучше. На настоящих снимках этот проход — лучший из
	// перепробованных для трудных случаев, поэтому идёт всегда.
	pick(small, sauvola...)
	if bestScore < enoughText {
		// почти ничего: страница вверх ногами, а определитель ориентации письмо не узнал
		// (мелкий шрифт, тень) — так было с оборотом, снятым перевёрнутым телефоном
		pick(rotate(small, 2), sauvola...)
		pick(rotate(big, 2))
	}
	return best, nil
}

// enoughText — сколько уверенно прочитанных знаков считать удачным распознаванием; меньше —
// пробуем перевёрнутый снимок. На настоящих титулах и карточках удачное — от сотни знаков.
const enoughText = 100

// smallSide — масштаб прохода с местным порогом.
const smallSide = 1200

// score — сколько знаков в строках, прочитанных уверенно (≥ 60 из 100): мера, по которой
// из проходов выбирается лучший. Мусор от неверного порога уверенности не набирает.
// Годный ISBN (контрольная цифра сходится) весит как целый абзац: по нему Филин узнаёт
// книгу, и проход, потерявший номер ради пары лишних слов, хуже (замер: оборот Степкина —
// обычный проход с ISBN, местный порог без него и с чуть большим числом знаков).
func score(tsv []byte) int {
	page := ParseTSV(tsv)
	n := 0
	for _, l := range page.Lines {
		if l.Conf >= 60 {
			n += len([]rune(strings.Join(strings.Fields(l.Text), "")))
		}
	}
	// номер ищется в самих строках: разбор титула поле ISBN не заполняет, а оборот с номером
	// и УДК часто опознаётся титулом
	seen := map[string]bool{}
	for _, l := range page.Lines {
		labeled := reISBNLabel.MatchString(l.Text)
		for _, c := range reISBNish.FindAllString(l.Text, -1) {
			// 10 цифр без подписи «ISBN» — скорее индекс издания или код («0503010000—006»):
			// сумма у случайных десяти цифр сходится в каждом одиннадцатом случае
			if !labeled && len(reNotDigitX.ReplaceAllString(c, "")) == 10 {
				continue
			}
			if g, err := isbn.Parse(c); err == nil && g.Valid && !seen[g.Norm] {
				seen[g.Norm] = true
				n += isbnWeight
			}
		}
	}
	return n
}

// isbnWeight — прибавка к оценке прохода за каждый годный ISBN.
const isbnWeight = 300

// reISBNish — то, что может быть ISBN внутри строки: 10 или 13 цифр с дефисами и пробелами,
// последняя может быть X (и кириллическая Х — так её читает Tesseract с русской моделью).
var reISBNish = regexp.MustCompile(`\d(?:[\d\-‐–— ]{8,17})[\dXxХх]`)

var (
	reISBNLabel = regexp.MustCompile(`(?i)` + isbnLabel) // «ISBN», как его видит распознавание (card.go)
	reNotDigitX = regexp.MustCompile(`[^\dXxХх]`)
)

// run — один вызов tesseract над картинкой; extra — дополнительные настройки.
func (e Engine) run(ctx context.Context, g image.Image, extra ...string) ([]byte, error) {
	var in bytes.Buffer
	if err := png.Encode(&in, g); err != nil {
		return nil, err
	}
	args := append([]string{"stdin", "stdout", "-l", "rus", "--psm", "3"}, extra...)
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.bin(), append(args, "tsv")...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &in, &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("распознавание не уложилось по времени: %w", ctx.Err())
		}
		return nil, fmt.Errorf("tesseract: %v: %s", err, firstLine(stderr.String()))
	}
	return out.Bytes(), nil
}

var (
	reRotate     = regexp.MustCompile(`Rotate:\s*(\d+)`)
	reOrientConf = regexp.MustCompile(`Orientation confidence:\s*([\d.]+)`)
)

// minOrientConf — уверенность определителя ориентации, с которой ему верим: ниже — на
// странице мало текста, и поворот наугад испортил бы снимок, который и так стоял прямо.
const minOrientConf = 1.5

// orientation — на сколько четвертей оборота по часовой стрелке повернуть снимок, чтобы
// текст встал прямо: телефон держат и вверх ногами, и боком, а в EXIF это не попадает.
// Определитель ориентации Tesseract (osd) не установлен или не уверен — 0, снимок как есть.
func (e Engine) orientation(ctx context.Context, g *image.Gray) int {
	var in bytes.Buffer
	if err := png.Encode(&in, g); err != nil {
		return 0
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, e.bin(), "stdin", "stdout", "--psm", "0", "-l", "osd")
	cmd.Stdin, cmd.Stdout = &in, &out
	if err := cmd.Run(); err != nil {
		return 0 // «слишком мало текста» или нет osd.traineddata
	}
	return parseOSD(out.String())
}

// parseOSD читает вывод osd: «Rotate: N» — на сколько градусов повернуть по часовой стрелке
// (направление проверено замером: титул, повёрнутый на 90/180/270, читается целиком).
func parseOSD(osd string) int {
	rm, cm := reRotate.FindStringSubmatch(osd), reOrientConf.FindStringSubmatch(osd)
	if rm == nil || cm == nil {
		return 0
	}
	if conf, _ := strconv.ParseFloat(cm[1], 64); conf < minOrientConf {
		return 0
	}
	switch rm[1] {
	case "90":
		return 1
	case "180":
		return 2
	case "270":
		return 3
	}
	return 0
}

// rotate поворачивает на quarter четвертей оборота по часовой стрелке.
func rotate(g *image.Gray, quarter int) *image.Gray {
	b := g.Bounds()
	w, h := b.Dx(), b.Dy()
	var out *image.Gray
	if quarter%2 == 1 {
		out = image.NewGray(image.Rect(0, 0, h, w))
	} else {
		out = image.NewGray(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := g.GrayAt(b.Min.X+x, b.Min.Y+y)
			switch quarter {
			case 1:
				out.SetGray(h-1-y, x, v)
			case 2:
				out.SetGray(w-1-x, h-1-y, v)
			case 3:
				out.SetGray(y, w-1-x, v)
			default:
				out.SetGray(x, y, v)
			}
		}
	}
	return out
}



// gray — оттенки серого: цвет бумаги и краски распознаванию только мешает.
func gray(img image.Image) *image.Gray {
	g := image.NewGray(img.Bounds())
	draw.Draw(g, g.Bounds(), img, img.Bounds().Min, draw.Src)
	return g
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return "без пояснений"
}
