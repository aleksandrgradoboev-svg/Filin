package paddle

import (
	"image"
	"math"
	"strings"
)

// Чтение строки (модель SVTR/CRNN из PaddleOCR) — как rapidocr 3.10: строка вырезается по
// рамке, приводится к высоте 48, ширина — не меньше 320, нормировка как у поиска строк;
// ответ — вероятности знаков по столбцам, расшифровка CTC (повторы схлопнуть, пустой знак
// выкинуть).

const (
	recH        = 48
	recMinW     = 320
	recMinScore = 0.5 // Global.text_score: строка с меньшей средней уверенностью отбрасывается
)

// crop — строка из снимка по рамке, выпрямленная (повёрнутый прямоугольник → ровный).
func crop(src *image.RGBA, r rect) *image.RGBA {
	c := r.corners()
	w := int(math.Max(dist(c[0], c[1]), dist(c[3], c[2])))
	h := int(math.Max(dist(c[0], c[3]), dist(c[1], c[2])))
	w, h = max(w, 1), max(h, 1)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	for y := 0; y < h; y++ {
		fy := (float64(y) + 0.5) / float64(h)
		for x := 0; x < w; x++ {
			fx := (float64(x) + 0.5) / float64(w)
			// точка внутри прямоугольника: от левого верхнего угла вдоль сторон
			px := c[0].X + fx*(c[1].X-c[0].X) + fy*(c[3].X-c[0].X) - 0.5
			py := c[0].Y + fx*(c[1].Y-c[0].Y) + fy*(c[3].Y-c[0].Y) - 0.5
			x0, y0 := int(math.Floor(px)), int(math.Floor(py))
			wx, wy := px-float64(x0), py-float64(y0)
			x0c, x1c := clampInt(x0, 0, sw-1), clampInt(x0+1, 0, sw-1)
			y0c, y1c := clampInt(y0, 0, sh-1), clampInt(y0+1, 0, sh-1)
			i00, i01 := src.PixOffset(x0c, y0c), src.PixOffset(x1c, y0c)
			i10, i11 := src.PixOffset(x0c, y1c), src.PixOffset(x1c, y1c)
			o := out.PixOffset(x, y)
			for k := 0; k < 3; k++ {
				v := (1-wy)*((1-wx)*float64(src.Pix[i00+k])+wx*float64(src.Pix[i01+k])) +
					wy*((1-wx)*float64(src.Pix[i10+k])+wx*float64(src.Pix[i11+k]))
				out.Pix[o+k] = uint8(math.Round(v))
			}
			out.Pix[o+3] = 255
		}
	}
	// высокая узкая рамка — строка, повёрнутая на четверть: как в эталоне, повернуть
	if float64(h)/float64(w) >= 1.5 {
		out = rot90ccw(out)
	}
	return out
}

func dist(a, b pt) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

func rot90ccw(src *image.RGBA) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			copy(out.Pix[out.PixOffset(y, w-1-x):][:4], src.Pix[src.PixOffset(x, y):][:4])
		}
	}
	return out
}

// read — текст строки и средняя уверенность 0–1.
func (e *Engine) read(line *image.RGBA) (string, float64, error) {
	w, h := line.Rect.Dx(), line.Rect.Dy()
	ratio := float64(w) / float64(h)
	padW := int(recH * math.Max(float64(recMinW)/recH, ratio))
	rw := min(int(math.Ceil(recH*ratio)), padW)
	rw = max(rw, 1)
	in := resizeRGBA(line, rw, recH)
	data := chwBGR(in, rw, recH, padW) // правее строки — нули, как padding_im в эталоне
	out, shape, err := e.rec.run(data, []int64{1, 3, recH, int64(padW)})
	if err != nil {
		return "", 0, err
	}
	steps, classes := int(shape[1]), int(shape[2])
	var b strings.Builder
	prev, sum, n := -1, 0.0, 0
	for t := 0; t < steps; t++ {
		row := out[t*classes : (t+1)*classes]
		best := 0
		for k := 1; k < classes; k++ {
			if row[k] > row[best] {
				best = k
			}
		}
		if best != 0 && best != prev {
			b.WriteString(e.label(best))
			sum += float64(row[best])
			n++
		}
		prev = best
	}
	if n == 0 {
		return "", 0, nil
	}
	return b.String(), sum / float64(n), nil
}

// label — знак по номеру выхода: 0 — пустой знак CTC, дальше словарь модели, последним — пробел.
func (e *Engine) label(k int) string {
	switch {
	case k <= 0:
		return ""
	case k <= len(e.chars):
		return e.chars[k-1]
	default:
		return " "
	}
}
