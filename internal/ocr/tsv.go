// Package ocr читает титульный лист с фото: Tesseract распознаёт строки с их местом
// на странице, а Parse раскладывает их по полям по канону советского титула — автор
// сверху, крупнее всего название, внизу город, издательство, год. Итог — черновик:
// его проверяет и правит человек, в базу без глаз ничего не пишется.
package ocr

import (
	"bufio"
	"bytes"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Line — строка текста, как её нашёл Tesseract: слова через пробел и рамка в пикселях.
type Line struct {
	Text   string
	Left   int
	Top    int
	Width  int
	Height int     // медиана высоты слов — по ней видно, какая строка крупнее
	Conf   float64 // средняя уверенность по словам, 0–100

	// середины первого и последнего слова по высоте: на фото с наклоном рамка строки
	// выше её правого конца, и продолжение строки ищется от конца, а не от рамки
	firstMid, lastMid int
}

// Page — распознанная страница: строки сверху вниз и размер картинки.
type Page struct {
	Width, Height int
	Lines         []Line
}

// ParseTSV разбирает вывод `tesseract … tsv`: строка таблицы на слово, со столбцами
// level page_num block_num par_num line_num word_num left top width height conf text.
func ParseTSV(data []byte) Page {
	type key struct{ block, par, line int }
	type acc struct {
		words   []string
		heights []int
		conf    float64
		n       int
		l, t    int
		r, b    int
		first   int // середина первого слова по высоте
		last    int // и последнего
	}
	var p Page
	lines := map[key]*acc{}
	var order []key
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Split(strings.TrimRight(sc.Text(), "\r"), "\t")
		if len(f) < 12 {
			continue
		}
		num := make([]int, 10)
		ok := true
		for i := 0; i < 10; i++ {
			v, err := strconv.Atoi(f[i])
			if err != nil {
				ok = false // заголовок таблицы
				break
			}
			num[i] = v
		}
		if !ok {
			continue
		}
		level, left, top, w, h := num[0], num[6], num[7], num[8], num[9]
		if level == 1 {
			p.Width, p.Height = w, h
			continue
		}
		text := strings.TrimSpace(f[11])
		if level != 5 || text == "" {
			continue
		}
		k := key{num[2], num[3], num[4]}
		a := lines[k]
		if a == nil {
			a = &acc{l: left, t: top, r: left + w, b: top + h, first: top + h/2}
			lines[k] = a
			order = append(order, k)
		}
		a.last = top + h/2
		a.words = append(a.words, text)
		a.heights = append(a.heights, h)
		if c, err := strconv.ParseFloat(f[10], 64); err == nil && c >= 0 {
			a.conf += c
			a.n++
		}
		a.l, a.t = min(a.l, left), min(a.t, top)
		a.r, a.b = max(a.r, left+w), max(a.b, top+h)
	}
	for _, k := range order {
		a := lines[k]
		ln := Line{Text: strings.Join(a.words, " "), Left: a.l, Top: a.t, Width: a.r - a.l, Height: median(a.heights),
			firstMid: a.first, lastMid: a.last}
		if a.n > 0 {
			ln.Conf = a.conf / float64(a.n)
		}
		p.Lines = append(p.Lines, ln)
	}
	sort.SliceStable(p.Lines, func(i, j int) bool { return p.Lines[i].Top < p.Lines[j].Top })
	p.Lines = mergeRows(p.Lines)
	return p
}

// mergeRows склеивает куски одной строки страницы. На фото под углом Tesseract режет
// абзац на колонки-блоки, и строка «второй мировой войны — выса|дке англо-американских»
// приходит двумя строками разных блоков. Кусок справа на той же высоте — продолжение;
// почти без промежутка — продолжение того же слова.
func mergeRows(lines []Line) []Line {
	var out []Line
	used := make([]bool, len(lines))
	for i := range lines {
		if used[i] {
			continue
		}
		used[i] = true // иначе строку, уже взятую началом, подхватит как продолжение другая
		cur := lines[i]
		for {
			best := -1
			for j := range lines {
				if used[j] || !sameRow(cur, lines[j]) || lines[j].Left < cur.Left+cur.Width-cur.Height/2 {
					continue
				}
				if best < 0 || lines[j].Left < lines[best].Left {
					best = j
				}
			}
			if best < 0 {
				break
			}
			used[best] = true
			next := lines[best]
			gap := next.Left - (cur.Left + cur.Width)
			sep := " "
			if gap < max(cur.Height, next.Height)/5 && endsLower(cur.Text) && startsLower(next.Text) {
				sep = ""
			}
			cur.Conf = (cur.Conf + next.Conf) / 2
			cur.Text += sep + next.Text
			right := next.Left + next.Width
			cur.Top = min(cur.Top, next.Top)
			cur.Width = right - cur.Left
			cur.lastMid = next.lastMid
		}
		out = append(out, cur)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Top < out[j].Top })
	return out
}

// sameRow — b продолжает a: конец a и начало b на одной высоте, расхождение меньше
// половины строки. Соседняя строка абзаца ниже на полторы высоты и больше.
func sameRow(a, b Line) bool {
	d := a.lastMid - b.firstMid
	if d < 0 {
		d = -d
	}
	return d < min(a.Height, b.Height)/2
}

func endsLower(s string) bool {
	rs := []rune(s)
	return len(rs) > 0 && unicode.IsLower(rs[len(rs)-1])
}

func startsLower(s string) bool {
	for _, r := range s {
		return unicode.IsLower(r)
	}
	return false
}

func median(v []int) int {
	if len(v) == 0 {
		return 0
	}
	s := append([]int{}, v...)
	sort.Ints(s)
	return s[len(s)/2]
}
