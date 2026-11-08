package ocr

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
)

// Замер распознавания на настоящих снимках с телефона: титулы и каталожные карточки, как их
// снимает человек — с полкой в кадре, тенью от руки, иногда вверх ногами. Фото в репозиторий
// не кладутся (вес, личная библиотека), лежат у разработчика: FILIN_OCR_CORPUS=<папка с <проба>.jpg>.
// Ожидаемое списано с фотографий, а не с выдачи распознавания. Мера — доля ключевых строк
// (фамилия, название, город, издательство, год, ISBN), нашедшихся в распознанном тексте.
var corpus = map[string][]string{
	// «Операция „Оверлорд“» (Прогресс, 1989): оборот с карточкой и титул
	"20261031-221133-ae4819": {"Хастингс", "Оверлорд", "второй фронт", "Ржешевск", "Прогресс", "1989"},
	"20261031-221608-b6b056": {"Хастингс", "Оверлорд", "Москва", "Прогресс", "1989"},
	// М. Риджуэй, «Солдат» (Воениздат, 1958): титул, три снимка
	"20261031-223602-9a086e": soldier,
	"20261031-225024-cacd11": soldier,
	"20261031-225440-623003": soldier,
	// тот же титул, повёрнутый на четверть, пол- и три четверти оборота: проверка, что
	// определитель ориентации поворачивает в ту сторону
	"20261031-223602-9a086e@1": soldier,
	"20261031-223602-9a086e@2": soldier,
	"20261031-223602-9a086e@3": soldier,
	// Степкин, «Английское и французское… противостояние на озере Онтарио» — оборот
	"20261108-130118-e33704": {"9785600017153", "УДК", "ББК", "Степкин", "Онтарио", "Освего", "Митчард", "Чернышова", "Москва", "2017"},
	// Ю. В. Борисов, «Шарль-Морис Талейран» (Междунар. отношения, 1986): карточка в тени и три титула
	"20261108-135259-6a916e": {"Борисов", "Талейран", "Междунар", "1986", "320", "Дипломатия", "ББК"},
	"20261108-135340-3c2033": borisov,
	"20261108-135401-953f37": borisov,
	"20261108-135446-ab209f": borisov,
	// Е. В. Тарле, «Талейран. Из мемуаров Талейрана» (Республика, 1993): титул и карточка вверх ногами
	"20261108-135607-53c6fc": {"Тарле", "Талейран", "мемуаров", "Москва", "Издательство", "Республика", "1993"},
	"20261108-135652-6e24fe": {"Тарле", "Талейран", "мемуаров", "Республика", "1993", "317", "5250020089", "ББК", "академика"},
}

var (
	soldier = []string{"Риджуэй", "Солдат", "английского", "редакцией", "Мильштейна", "Военное издательство", "Министерства обороны", "Москва", "1958"}
	borisov = []string{"Борисов", "Шарль-Морис", "Талейран", "Москва", "Международные", "отношения", "1986"}
)

var reNotDigit = regexp.MustCompile(`\D`)

// rotatedJPEG — снимок, повёрнутый на q четвертей оборота по часовой стрелке, как если бы
// телефон держали боком.
func rotatedJPEG(t *testing.T, photo []byte, q int) []byte {
	img, err := cover.Decode(photo)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, rotate(gray(img), q), &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// found — нашлась ли ключевая строка: без регистра и «ё»; номер — по цифрам, пробелы и дефисы
// распознавания не в счёт.
func found(text, digits, want string) bool {
	if reNotDigit.ReplaceAllString(want, "") == want {
		return strings.Contains(digits, want)
	}
	return strings.Contains(text, foldKey(strings.Join(strings.Fields(want), " ")))
}

func foldKey(s string) string { return strings.ReplaceAll(strings.ToLower(s), "ё", "е") }

func TestCorpus(t *testing.T) {
	dir := os.Getenv("FILIN_OCR_CORPUS")
	if dir == "" {
		t.Skip("замер на настоящих снимках — только по FILIN_OCR_CORPUS=<папка>")
	}
	e := Engine{Bin: `C:\Program Files\Tesseract-OCR\tesseract.exe`}
	if !e.Available() {
		t.Skip("нет Tesseract")
	}
	names := make([]string, 0, len(corpus))
	for n := range corpus {
		names = append(names, n)
	}
	sort.Strings(names)
	total, hit := 0, 0
	only := os.Getenv("FILIN_OCR_ONLY") // одна-две пробы через запятую — для разбора
	for _, n := range names {
		if only != "" && !strings.Contains(only, n) {
			continue
		}
		base, quarter, _ := strings.Cut(n, "@")
		photo, err := os.ReadFile(filepath.Join(dir, base+".jpg"))
		if err != nil {
			t.Logf("%s: нет снимка", n)
			continue
		}
		if q, _ := strconv.Atoi(quarter); q > 0 {
			photo = rotatedJPEG(t, photo, q)
		}
		start := time.Now()
		tsv, err := e.Recognize(context.Background(), photo)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if save := os.Getenv("FILIN_OCR_SAVE"); save != "" { // таблица слов — в testdata для тестов разбора
			if err := os.WriteFile(filepath.Join(save, n+".tsv"), tsv, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		d := Parse(ParseTSV(tsv))
		if os.Getenv("FILIN_OCR_VERBOSE") != "" {
			t.Logf("%s строки:\n  %s", n, strings.Join(d.Lines, "\n  "))
			t.Logf("%s черновик: вид %s | %q / %q | авторы %q люди %v | %s: %s, %d | с. %d | ISBN %q ББК %q УДК %q знак %q | аннотация %d зн.",
				n, d.Kind, d.Title, d.Subtitle, d.Authors, d.People, d.City, d.Publisher, d.Year, d.Pages,
				d.ISBN, d.BBK, d.UDK, d.AuthorSign, len([]rune(d.Annotation)))
		}
		joined := strings.Join(d.Lines, " ")
		text := foldKey(strings.Join(strings.Fields(joined), " "))
		digits := reNotDigit.ReplaceAllString(joined, "")
		var miss []string
		for _, w := range corpus[n] {
			total++
			if found(text, digits, w) {
				hit++
			} else {
				miss = append(miss, w)
			}
		}
		t.Logf("%s %-5s %d/%d за %4.1fs  нет: %s", n, d.Kind, len(corpus[n])-len(miss), len(corpus[n]),
			time.Since(start).Seconds(), strings.Join(miss, ", "))
	}
	t.Logf("ИТОГО: %d из %d ключевых строк (%d%%)", hit, total, hit*100/max(total, 1))
}
