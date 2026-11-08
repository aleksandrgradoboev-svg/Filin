package paddle

import (
	"image"
	"image/draw"
	"math"
	"sort"
)

// Поиск строк (модель DB из PaddleOCR) — повторяет rapidocr 3.10: те же размеры, нормировка
// и разбор карты вероятностей, только без OpenCV.

const (
	maxSide      = 2000 // Global.max_side_len: больше — уменьшить
	detMinSide   = 736  // Det.limit_side_len при limit_type=min: меньше — увеличить
	detThresh    = 0.3  // порог карты вероятностей
	detBoxThresh = 0.5  // средняя вероятность внутри рамки — не ниже
	detUnclip    = 1.6  // расширение рамки
	detMinSize   = 3    // короткая сторона рамки — не меньше
)

// toRGBA — картинка в 8-битном RGBA, чтобы читать пиксели напрямую.
func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(r, r.Rect, img, b.Min, draw.Src)
	return r
}

// resizeRGBA — билинейное масштабирование с центрами пикселей, как cv2.INTER_LINEAR.
func resizeRGBA(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	fx, fy := float64(sw)/float64(w), float64(sh)/float64(h)
	for y := 0; y < h; y++ {
		sy := (float64(y)+0.5)*fy - 0.5
		y0 := int(math.Floor(sy))
		wy := sy - float64(y0)
		y0c, y1c := clampInt(y0, 0, sh-1), clampInt(y0+1, 0, sh-1)
		for x := 0; x < w; x++ {
			sx := (float64(x)+0.5)*fx - 0.5
			x0 := int(math.Floor(sx))
			wx := sx - float64(x0)
			x0c, x1c := clampInt(x0, 0, sw-1), clampInt(x0+1, 0, sw-1)
			i00, i01 := src.PixOffset(x0c, y0c), src.PixOffset(x1c, y0c)
			i10, i11 := src.PixOffset(x0c, y1c), src.PixOffset(x1c, y1c)
			o := dst.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				v := (1-wy)*((1-wx)*float64(src.Pix[i00+c])+wx*float64(src.Pix[i01+c])) +
					wy*((1-wx)*float64(src.Pix[i10+c])+wx*float64(src.Pix[i11+c]))
				dst.Pix[o+c] = uint8(math.Round(v))
			}
			dst.Pix[o+3] = 255
		}
	}
	return dst
}

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

// chwBGR — тензор [3,h,w] в порядке BGR (модели PaddleOCR учились на картинках OpenCV),
// нормировка (x/255 − 0.5)/0.5.
func chwBGR(img *image.RGBA, w, h, padW int) []float32 {
	out := make([]float32, 3*h*padW)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(x, y)
			for c := 0; c < 3; c++ { // c: 0 — B, 1 — G, 2 — R
				out[c*h*padW+y*padW+x] = (float32(img.Pix[o+2-c])/255 - 0.5) / 0.5
			}
		}
	}
	return out
}

// prepare — снимок до размера, с которым работает эталон (не больше 2000 по длинной стороне).
func prepare(img image.Image) *image.RGBA {
	src := toRGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if m := max(w, h); m > maxSide {
		r := float64(maxSide) / float64(m)
		src = resizeRGBA(src, int(float64(w)*r), int(float64(h)*r))
	}
	return src
}

// detect — рамки строк на снимке src (уже приготовленном prepare), в его координатах.
func (e *Engine) detect(src *image.RGBA) ([]rect, error) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	ratio := 1.0
	if m := min(w, h); m < detMinSide {
		ratio = float64(detMinSide) / float64(m)
	}
	rw := int(math.Round(float64(int(float64(w)*ratio))/32) * 32)
	rh := int(math.Round(float64(int(float64(h)*ratio))/32) * 32)
	in := resizeRGBA(src, rw, rh)
	pred, shape, err := e.det.run(chwBGR(in, rw, rh, rw), []int64{1, 3, int64(rh), int64(rw)})
	if err != nil {
		return nil, err
	}
	ph, pw := int(shape[2]), int(shape[3])

	// порог и расширение 2×2 (cv2.dilate с ядром 2×2: точка — максимум из себя, левой,
	// верхней и левой верхней)
	mask := make([]bool, ph*pw)
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			on := false
			for dy := -1; dy <= 0 && !on; dy++ {
				for dx := -1; dx <= 0 && !on; dx++ {
					if xx, yy := x+dx, y+dy; xx >= 0 && yy >= 0 && pred[yy*pw+xx] > detThresh {
						on = true
					}
				}
			}
			mask[y*pw+x] = on
		}
	}

	var boxes []rect
	seen := make([]bool, len(mask))
	stack := []int{}
	for start := range mask {
		if !mask[start] || seen[start] {
			continue
		}
		// связная область (8 соседей); точки контура — те, у кого есть сосед вне маски
		var border []pt
		seen[start] = true
		stack = append(stack[:0], start)
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%pw, i/pw
			edge := false
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					xx, yy := x+dx, y+dy
					if xx < 0 || yy < 0 || xx >= pw || yy >= ph {
						edge = true
						continue
					}
					j := yy*pw + xx
					if !mask[j] {
						edge = true
						continue
					}
					if !seen[j] {
						seen[j] = true
						stack = append(stack, j)
					}
				}
			}
			if edge {
				border = append(border, pt{float64(x), float64(y)})
			}
		}
		r := minAreaRect(border)
		if r.short() < detMinSize {
			continue
		}
		if boxScore(pred, pw, ph, r) < detBoxThresh {
			continue
		}
		r = r.unclip(detUnclip)
		if r.short() < detMinSize+2 {
			continue
		}
		// в координаты снимка
		sx, sy := float64(w)/float64(pw), float64(h)/float64(ph)
		r = scaleRect(r, sx, sy)
		if 2*r.HW <= 3 || 2*r.HH <= 3 {
			continue
		}
		boxes = append(boxes, r)
	}
	sortBoxes(boxes)
	return boxes, nil
}

// boxScore — средняя вероятность внутри рамки (score_mode=fast).
func boxScore(pred []float32, pw, ph int, r rect) float64 {
	c := r.corners()
	xmin, xmax, ymin, ymax := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, p := range c {
		xmin, xmax = math.Min(xmin, p.X), math.Max(xmax, p.X)
		ymin, ymax = math.Min(ymin, p.Y), math.Max(ymax, p.Y)
	}
	x0, x1 := clampInt(int(math.Floor(xmin)), 0, pw-1), clampInt(int(math.Ceil(xmax)), 0, pw-1)
	y0, y1 := clampInt(int(math.Floor(ymin)), 0, ph-1), clampInt(int(math.Ceil(ymax)), 0, ph-1)
	grow := r
	grow.HW += 0.5 // заливка многоугольника в OpenCV включает пиксели на самой границе
	grow.HH += 0.5
	sum, n := 0.0, 0
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if grow.contains(pt{float64(x), float64(y)}) {
				sum += float64(pred[y*pw+x])
				n++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// scaleRect — рамка в другом масштабе по осям (карта меньше снимка не во столько же раз
// по ширине, во сколько по высоте — из-за округления до 32).
func scaleRect(r rect, sx, sy float64) rect {
	c := r.corners()
	var p []pt
	for _, q := range c {
		p = append(p, pt{q.X * sx, q.Y * sy})
	}
	return minAreaRect(p)
}

// sortBoxes — сверху вниз, а в одной строке (разница по высоте меньше 10 пикселей) — слева
// направо, как sorted_boxes в эталоне.
func sortBoxes(b []rect) {
	tl := func(r rect) pt { return r.corners()[0] }
	sort.SliceStable(b, func(i, j int) bool {
		a, c := tl(b[i]), tl(b[j])
		return a.Y < c.Y || a.Y == c.Y && a.X < c.X
	})
	for i := 0; i < len(b)-1; i++ {
		for j := i; j >= 0; j-- {
			a, c := tl(b[j]), tl(b[j+1])
			if math.Abs(c.Y-a.Y) < 10 && c.X < a.X {
				b[j], b[j+1] = b[j+1], b[j]
			} else {
				break
			}
		}
	}
}
