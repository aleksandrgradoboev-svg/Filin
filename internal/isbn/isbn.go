// Package isbn разбирает ISBN так, как он встречается на книгах и в каталогах:
// с дефисами, пробелами, приставкой «ISBN», иногда с ошибкой в контрольной цифре.
//
// Неверная контрольная сумма — не повод выбросить номер: она бывает и опечаткой ввода,
// и браком печати. Поэтому Parse возвращает номер с признаком Valid, а решает вызывающий.
package isbn

import (
	"errors"
	"strings"
)

// ErrNotISBN — в строке нет ничего похожего на ISBN (пусто, «отсутствует», не та длина).
var ErrNotISBN = errors.New("не ISBN")

// ISBN — разобранный номер.
type ISBN struct {
	Raw   string // как было на входе
	Norm  string // только цифры и X; при верном ISBN-10 — уже переведён в 13 цифр
	Valid bool   // контрольная цифра сходится
}

// Parse чистит строку и проверяет контрольную цифру.
// Верный ISBN-10 переводится в ISBN-13, чтобы один и тот же номер искался одинаково.
// Неверный оставляется как есть: перевод пересчитал бы контрольную цифру и спрятал ошибку.
func Parse(raw string) (ISBN, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = trimLabel(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r == 'X':
			b.WriteRune(r)
		case r == 'Х': // кириллическая «Х», набранная в русской раскладке
			b.WriteByte('X')
		case strings.ContainsRune(separators, r):
		default:
			return ISBN{}, ErrNotISBN
		}
	}
	n := b.String()
	if i := strings.IndexByte(n, 'X'); i >= 0 && !(len(n) == 10 && i == 9) {
		return ISBN{}, ErrNotISBN
	}
	switch len(n) {
	case 10:
		if valid10(n) {
			return ISBN{Raw: raw, Norm: to13(n), Valid: true}, nil
		}
		return ISBN{Raw: raw, Norm: n}, nil
	case 13:
		return ISBN{Raw: raw, Norm: n, Valid: valid13(n)}, nil
	}
	return ISBN{}, ErrNotISBN
}

// separators — что встречается между группами цифр: дефисы и тире всех видов, пробелы.
const separators = "-:  ‐‑‒–—−"

// trimLabel срезает подпись перед номером: «ISBN», «ISBN-13:», «ISBN 10». Цифры 10/13 подписи
// иначе прилипли бы к номеру и сделали его длиной 15.
func trimLabel(s string) string {
	rest, ok := strings.CutPrefix(s, "ISBN")
	if !ok {
		return s
	}
	t := strings.TrimLeft(rest, separators)
	for _, n := range []string{"13", "10"} {
		if after, ok := strings.CutPrefix(t, n); ok {
			// «ISBN 13» — подпись, только если за ней разделитель, а не продолжение номера
			if after == "" || strings.ContainsRune(separators, []rune(after)[0]) {
				return after
			}
		}
	}
	return rest
}

// List разбирает строку с несколькими номерами через запятую, точку с запятой или косую
// черту — так пишут соиздания. Пробел номера не разделяет: он бывает внутри номера. Фрагмент без единой цифры («отсутствует») — это отсутствие номера и
// пропускается; фрагмент с цифрами, который не разобрался, возвращается в bad, чтобы
// вызывающий не потерял номер молча.
func List(raw string) (good []ISBN, bad []string) {
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '/' || r == '\n' }) {
		part = strings.TrimSpace(part)
		if !strings.ContainsAny(part, "0123456789") {
			continue
		}
		if n, err := Parse(part); err == nil {
			good = append(good, n)
		} else {
			bad = append(bad, part)
		}
	}
	return good, bad
}

// IsBookland — штрихкод книжный (978/979). Магазинная наклейка с EAN-13 другим префиксом
// проходит проверку суммы, но по ней искать нечего.
func IsBookland(norm string) bool {
	return len(norm) == 13 && (strings.HasPrefix(norm, "978") || strings.HasPrefix(norm, "979"))
}

func valid10(n string) bool {
	sum := 0
	for i := 0; i < 10; i++ {
		d := int(n[i] - '0')
		if n[i] == 'X' {
			d = 10
		}
		sum += d * (10 - i)
	}
	return sum%11 == 0
}

func valid13(n string) bool {
	return n[12]-'0' == check13(n[:12])
}

func check13(first12 string) byte {
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(first12[i] - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return byte((10 - sum%10) % 10)
}

func to13(n10 string) string {
	p := "978" + n10[:9]
	return p + string('0'+check13(p))
}
