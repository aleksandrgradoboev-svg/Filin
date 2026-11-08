package paddle

import (
	"math"
	"sort"
)

// Геометрия поиска строк — то, что в эталоне (rapidocr) делает OpenCV: выпуклая оболочка
// точек области, наименьший по площади повёрнутый прямоугольник и его расширение.

type pt struct{ X, Y float64 }

// hull — выпуклая оболочка (монотонная цепь Эндрю), против часовой стрелки.
func hull(p []pt) []pt {
	if len(p) < 3 {
		return append([]pt{}, p...)
	}
	s := append([]pt{}, p...)
	sort.Slice(s, func(i, j int) bool { return s[i].X < s[j].X || s[i].X == s[j].X && s[i].Y < s[j].Y })
	cross := func(o, a, b pt) float64 { return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X) }
	h := make([]pt, 0, 2*len(s))
	for _, q := range s {
		for len(h) >= 2 && cross(h[len(h)-2], h[len(h)-1], q) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, q)
	}
	lo := len(h) + 1
	for i := len(s) - 2; i >= 0; i-- {
		q := s[i]
		for len(h) >= lo && cross(h[len(h)-2], h[len(h)-1], q) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, q)
	}
	return h[:len(h)-1]
}

// rect — повёрнутый прямоугольник: центр, оси (единичные u, v) и полуразмеры.
type rect struct {
	C      pt
	U, V   pt      // u — вдоль длинной стороны, v — перпендикуляр
	HW, HH float64 // половина длины и половина высоты
}

// minAreaRect — наименьший прямоугольник вокруг точек: одна из сторон лежит на ребре
// выпуклой оболочки, перебираются все рёбра.
func minAreaRect(p []pt) rect {
	h := hull(p)
	if len(h) == 1 {
		return rect{C: h[0], U: pt{1, 0}, V: pt{0, 1}}
	}
	best, bestArea := rect{}, math.Inf(1)
	for i := range h {
		a, b := h[i], h[(i+1)%len(h)]
		dx, dy := b.X-a.X, b.Y-a.Y
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		u := pt{dx / l, dy / l}
		v := pt{-u.Y, u.X}
		minU, maxU, minV, maxV := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
		for _, q := range h {
			pu, pv := q.X*u.X+q.Y*u.Y, q.X*v.X+q.Y*v.Y
			minU, maxU = math.Min(minU, pu), math.Max(maxU, pu)
			minV, maxV = math.Min(minV, pv), math.Max(maxV, pv)
		}
		if area := (maxU - minU) * (maxV - minV); area < bestArea {
			bestArea = area
			cu, cv := (minU+maxU)/2, (minV+maxV)/2
			best = rect{C: pt{cu*u.X + cv*v.X, cu*u.Y + cv*v.Y}, U: u, V: v, HW: (maxU - minU) / 2, HH: (maxV - minV) / 2}
		}
	}
	// длинная сторона — по u, и u смотрит вправо: строка читается слева направо
	if best.HH > best.HW {
		best.U, best.V = best.V, pt{-best.V.X, -best.V.Y}
		best.HW, best.HH = best.HH, best.HW
	}
	if best.U.X < 0 || best.U.X == 0 && best.U.Y < 0 {
		best.U, best.V = pt{-best.U.X, -best.U.Y}, pt{-best.V.X, -best.V.Y}
	}
	if best.V.Y < 0 { // v — вниз, как ось y картинки
		best.V = pt{-best.V.X, -best.V.Y}
	}
	return best
}

// corners — углы: левый верхний, правый верхний, правый нижний, левый нижний.
func (r rect) corners() [4]pt {
	at := func(su, sv float64) pt {
		return pt{r.C.X + su*r.HW*r.U.X + sv*r.HH*r.V.X, r.C.Y + su*r.HW*r.U.Y + sv*r.HH*r.V.Y}
	}
	return [4]pt{at(-1, -1), at(1, -1), at(1, 1), at(-1, 1)}
}

// unclip — расширение рамки, как в DB: на расстояние площадь·k/периметр со всех сторон.
// У прямоугольника со скруглёнными углами наименьший прямоугольник вокруг — просто шире
// и выше на это расстояние с каждой стороны.
func (r rect) unclip(k float64) rect {
	w, h := 2*r.HW, 2*r.HH
	d := w * h * k / (2 * (w + h))
	r.HW += d
	r.HH += d
	return r
}

// short — короткая сторона.
func (r rect) short() float64 { return 2 * math.Min(r.HW, r.HH) }

// contains — точка внутри прямоугольника (для средней уверенности по рамке).
func (r rect) contains(q pt) bool {
	dx, dy := q.X-r.C.X, q.Y-r.C.Y
	return math.Abs(dx*r.U.X+dy*r.U.Y) <= r.HW && math.Abs(dx*r.V.X+dy*r.V.Y) <= r.HH
}
