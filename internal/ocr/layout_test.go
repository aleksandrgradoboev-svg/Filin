package ocr

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// row — строка титула для синтетического вывода Tesseract: текст, верх и высота букв.
type row struct {
	text   string
	top, h int
	conf   int // 0 — 90
}

// tsv собирает вывод `tesseract … tsv` для страницы 2000×3000: строка — отдельный блок.
func tsv(rows ...row) []byte {
	var b strings.Builder
	b.WriteString("level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n")
	b.WriteString("1\t1\t0\t0\t0\t0\t0\t0\t2000\t3000\t-1\t\n")
	for i, r := range rows {
		c := r.conf
		if c == 0 {
			c = 90
		}
		fmt.Fprintf(&b, "4\t1\t%d\t1\t1\t0\t300\t%d\t1400\t%d\t-1\t\n", i+1, r.top, r.h)
		x := 300
		for j, w := range strings.Fields(r.text) {
			wd := len([]rune(w)) * r.h * 2 / 3
			fmt.Fprintf(&b, "5\t1\t%d\t1\t1\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", i+1, j+1, x, r.top, wd, r.h, c, w)
			x += wd + r.h/2
		}
	}
	return []byte(b.String())
}

func TestTitleWithOrganizationOnTop(t *testing.T) {
	d := Parse(ParseTSV(tsv(
		row{"АКАДЕМИЯ НАУК СССР", 200, 40, 0},
		row{"Е. В. ТАРЛЕ", 450, 50, 0},
		row{"НАШЕСТВИЕ", 900, 120, 0},
		row{"НАПОЛЕОНА", 1060, 120, 0},
		row{"НА РОССИЮ", 1220, 120, 0},
		row{"1812 год", 1450, 50, 0},
		row{"ОГИЗ", 2600, 45, 0},
		row{"ГОСУДАРСТВЕННОЕ СОЦИАЛЬНО-ЭКОНОМИЧЕСКОЕ ИЗДАТЕЛЬСТВО", 2660, 35, 0},
		row{"МОСКВА · 1938", 2750, 40, 0},
	)))
	want := Draft{
		Kind:      KindTitle,
		People:    []Person{},
		ISBN:      []string{},
		Title:     "Нашествие наполеона на россию",
		Subtitle:  "1812 год",
		Authors:   []string{"Е. В. Тарле"},
		Publisher: "Государственное социально-экономическое издательство",
		City:      "Москва",
		Year:      1938,
		Others:    []string{"Академия наук СССР"},
	}
	d.Lines = nil
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("черновик\n got %+v\nwant %+v", d, want)
	}
}

func TestQuotedPublisherAbbreviatedCityTwoAuthors(t *testing.T) {
	d := Parse(ParseTSV(tsv(
		row{"И. ИЛЬФ и Е. ПЕТРОВ", 400, 55, 0},
		row{"ДВЕНАДЦАТЬ", 1000, 130, 0},
		row{"СТУЛЬЕВ", 1170, 130, 0},
		row{"РОМАН", 1400, 45, 0},
		row{"Предисловие А. Вулиса", 1700, 35, 0},
		row{"М. «Художественная литература» 1987", 2700, 40, 0},
	)))
	if d.Title != "Двенадцать стульев" || d.Subtitle != "Роман" {
		t.Errorf("название %q, подзаголовок %q", d.Title, d.Subtitle)
	}
	if !reflect.DeepEqual(d.Authors, []string{"И. Ильф", "Е. Петров"}) {
		t.Errorf("авторы %q", d.Authors)
	}
	if d.Publisher != "Художественная литература" || d.City != "Москва" || d.Year != 1987 {
		t.Errorf("выходные данные: %q, %q, %d", d.Publisher, d.City, d.Year)
	}
	if !reflect.DeepEqual(d.Others, []string{"Предисловие А. Вулиса"}) {
		t.Errorf("прочее %q", d.Others)
	}
}

func TestWrappedTitleAndNoise(t *testing.T) {
	d := Parse(ParseTSV(tsv(
		row{"~", 100, 30, 20},
		row{"А. С. Пушкин", 400, 50, 0},
		row{"Воспомина-", 900, 110, 0},
		row{"ния", 1040, 110, 0},
		row{"шш ии", 1800, 30, 12}, // пятно на странице
		row{"Ленинград", 2650, 40, 0},
		row{"Лениздат", 2710, 40, 0},
		row{"1974", 2770, 40, 0},
	)))
	if d.Title != "Воспоминания" {
		t.Errorf("название %q", d.Title)
	}
	if !reflect.DeepEqual(d.Authors, []string{"А. С. Пушкин"}) {
		t.Errorf("авторы %q", d.Authors)
	}
	if d.City != "Ленинград" || d.Publisher != "Лениздат" || d.Year != 1974 {
		t.Errorf("выходные данные: %q, %q, %d", d.Publisher, d.City, d.Year)
	}
	if d.Subtitle != "" || len(d.Others) != 0 {
		t.Errorf("мусор попал в поля: подзаголовок %q, прочее %q", d.Subtitle, d.Others)
	}
	if len(d.Lines) != 8 {
		t.Errorf("весь текст для сверки — %d строк, ждали 8", len(d.Lines))
	}
}

func TestYearInTitleIsNotImprint(t *testing.T) {
	// год в названии («1812») выше середины страницы — это не год издания
	d := Parse(ParseTSV(tsv(
		row{"1812", 900, 130, 0},
		row{"Москва", 2700, 40, 0},
	)))
	if d.Year != 0 || d.Title != "1812" || d.City != "Москва" {
		t.Errorf("год %d, название %q, город %q", d.Year, d.Title, d.City)
	}
}

func TestEmptyPage(t *testing.T) {
	d := Parse(ParseTSV(tsv()))
	if d.Title != "" || d.Authors == nil || d.Others == nil || d.Lines == nil {
		t.Errorf("пустая страница: %+v — списки должны быть пустыми, а не nil (уходят в JSON)", d)
	}
}
