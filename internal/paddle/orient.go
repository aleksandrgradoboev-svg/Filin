package paddle

import (
	"image"
)

// Ориентация страницы — модель PP-LCNet_x1_0_doc_ori: на сколько четвертей по часовой
// стрелке повёрнут снимок (0, 90, 180, 270°). Подготовка — по её inference.yml: короткая
// сторона 256, середина 224×224, нормировка ImageNet. Порядок цветов и смысл меток
// определены замером на 64 снимках с известным ответом (16 настоящих × 4 поворота): метка k —
// страница повёрнута на k четвертей по часовой; верно 62 из 64, ошибки — 90° против 270°.

var (
	oriMean = [3]float32{0.485, 0.456, 0.406} // R, G, B
	oriStd  = [3]float32{0.229, 0.224, 0.225}
)

// orientation — на сколько четвертей по часовой стрелке повёрнута страница.
func (e *Engine) orientation(src *image.RGBA) (int, error) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	k := 256 / float64(min(w, h))
	rw, rh := int(float64(w)*k+0.5), int(float64(h)*k+0.5)
	small := resizeRGBA(src, rw, rh)
	x0, y0 := (rw-224)/2, (rh-224)/2
	data := make([]float32, 3*224*224)
	for y := 0; y < 224; y++ {
		for x := 0; x < 224; x++ {
			o := small.PixOffset(x0+x, y0+y)
			for c := 0; c < 3; c++ { // RGB
				data[c*224*224+y*224+x] = (float32(small.Pix[o+c])/255 - oriMean[c]) / oriStd[c]
			}
		}
	}
	out, _, err := e.ori.run(data, []int64{1, 3, 224, 224})
	if err != nil {
		return 0, err
	}
	best := 0
	for i := 1; i < len(out); i++ {
		if out[i] > out[best] {
			best = i
		}
	}
	return best, nil
}

// rotateCW — поворот на q четвертей по часовой стрелке.
func rotateCW(src *image.RGBA, q int) *image.RGBA {
	q = ((q % 4) + 4) % 4
	if q == 0 {
		return src
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	var dst *image.RGBA
	if q%2 == 1 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch q {
			case 1:
				dx, dy = h-1-y, x
			case 2:
				dx, dy = w-1-x, h-1-y
			case 3:
				dx, dy = y, w-1-x
			}
			copy(dst.Pix[dst.PixOffset(dx, dy):][:4], src.Pix[src.PixOffset(x, y):][:4])
		}
	}
	return dst
}
