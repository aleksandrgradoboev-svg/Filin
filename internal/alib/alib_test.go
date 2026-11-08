package alib

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// Адреса, проверенные на живом alib 06.11.2026: каждый открыл страницу книги.
func TestPageURL(t *testing.T) {
	for _, c := range []struct{ surname, title, want string }{
		{"Куликовский", "Справочник любителя астрономии", "au-kulikovskij/nm-spravochnik_lyubitelya_astronomii/"},
		{"Чуковский", "Водители фрегатов", "au-chukovskij/nm-voditeli_fregatov/"},
		{"Риджуэй", "Солдат", "au-ridzhuqj/nm-soldat/"},
		{"Хастингс", "Операция «Оверлорд»", "au-hastings/nm-operaciya_overlord/"},
		{"Зигель", "Сокровища звездного неба", "au-zigelnmn/nm-sokroviwa_zvezdnogo_neba/"},
		{"Заозерский", "Фельдмаршал Б. П. Шереметев", "au-zaozerskij/nm-felnmndmarshal_b_p_sheremetev/"},
		{"Керес", "Сто партий", "au-keres/nm-sto_partij/"},
		{"Петросян", "Стратегия надёжности", "au-petrosyan/nm-strategiya_nadezhnosti/"},
		{"Хорьков", "Грозовой июнь", "au-hornmnkov/nm-grozovoj_iyunnmn/"},
		{"Циолковский", "Путь к звездам", "au-ciolkovskij/nm-putnmn_zvezdam/"},
		{"", "Правила ношения военной формы одежды", "au-/nm-pravila_nosheniya_voennoj_formy_odezhdy/"},
	} {
		if got := PageURL(c.surname, c.title); got != "https://www.alib.ru/"+c.want {
			t.Errorf("%s «%s»: %s", c.surname, c.title, got)
		}
	}
}

func TestSurnameAndTitle(t *testing.T) {
	for _, c := range []struct {
		name  string
		given bool
		want  string
	}{
		{"Петр Куликовский", true, "Куликовский"},
		{"Риджуэй Мэтью", false, "Риджуэй"},
		{"Дубровин Николай Федорович", false, "Дубровин"},
		{"С. Б. Губницкий", true, "Губницкий"},
		{"А. И. Заозерский", false, "Заозерский"},
		{"", true, ""},
	} {
		if got := Surname(c.name, c.given); got != c.want {
			t.Errorf("%q: %q", c.name, got)
		}
	}
	for in, want := range map[string]string{
		"Операция «Оверлорд»: Как был открыт второй фронт":            "Операция «Оверлорд»",
		"Водители фрегатов: Книга о великих мореплавателях (сборник)": "Водители фрегатов",
		"Философия в будуаре. Тереза-философ (сборник)":               "Философия в будуаре. Тереза-философ",
		"Солдат / пер. с англ.":       "Солдат",
		"Фельдмаршал Б. П. Шереметев": "Фельдмаршал Б. П. Шереметев",
		"(Не)интересная история":      "(Не)интересная история",
	} {
		if got := ShortTitle(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func load(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := charmap.Windows1251.NewDecoder().Bytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(s)
}

func TestParse(t *testing.T) {
	p := Parse(load(t, "hastings.html"), "u")
	if !p.Found() || !strings.HasPrefix(p.Annotation, "В книге рассказывается о самой крупной десантной операции") ||
		!strings.Contains(p.Annotation, `из "горячих точек"`) || !strings.HasPrefix(p.Heading, "Хастингс Макс. Операция Оверлорд") {
		t.Errorf("шапка: %q / %q", p.Heading, p.Annotation)
	}
	if len(p.Offers) == 0 {
		t.Fatal("нет предложений")
	}
	o := p.Offers[0]
	if o.Seller != "BS-продавец1 Кунгур." || !strings.Contains(o.Desc, "Прогресс. 1989г.") || o.Condition != "хорошее, штамп" ||
		o.Price != "170 руб." || !strings.HasPrefix(o.URL, "https://www.alib.ru/5_hastings") ||
		o.Rub != 170 || o.Year != 1989 {
		t.Errorf("первое предложение: %+v", o)
	}

	k := Parse(load(t, "kulikovskij.html"), "u")
	// 11 — столько же в разметке schema.org той же страницы (offerCount)
	if len(k.Offers) != 11 || !strings.HasPrefix(k.Annotation, "В справочнике излагаются задачи") {
		t.Errorf("Куликовский: %d предложений, %q", len(k.Offers), k.Annotation[:40])
	}
	for _, o := range k.Offers {
		if strings.Contains(o.Desc, "фото") || o.Desc == "" || o.Price == "" || o.Rub <= 0 {
			t.Errorf("предложение разобрано криво: %+v", o)
		}
	}

	// продавец без строки «<b>BS-…</b> Город.»: описание не должно уехать в имя
	c := Parse(load(t, "chukovskij.html"), "u")
	if len(c.Offers) < 40 {
		t.Fatalf("Чуковский: %d предложений", len(c.Offers))
	}
	for _, o := range c.Offers {
		if o.Desc == "" || strings.Contains(o.Seller, "литература") {
			t.Errorf("предложение разобрано криво: %+v", o)
		}
	}

	if e := Parse(load(t, "empty.html"), "u"); e.Found() {
		t.Errorf("пустая страница принята за книгу: %+v", e)
	}
}

// Клиент: адрес, честный User-Agent, перекодировка и пауза между запросами.
func TestClient(t *testing.T) {
	page, _ := os.ReadFile("testdata/hastings.html")
	var paths []string
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		times = append(times, time.Now())
		if r.UserAgent() != UserAgent {
			t.Errorf("User-Agent: %q", r.UserAgent())
		}
		w.Write(page)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Delay: 150 * time.Millisecond}
	// та же книга дважды — второй раз из памяти, без запроса
	for range 2 {
		p, err := c.Fetch(context.Background(), "Хастингс", "Операция «Оверлорд»: Как был открыт второй фронт")
		if err != nil || !p.Found() || p.URL != "https://www.alib.ru/au-hastings/nm-operaciya_overlord_kak_byl_otkryt_vtoroj_front/" {
			t.Fatalf("%v %+v", err, p.URL)
		}
	}
	if len(paths) != 1 || paths[0] != "/au-hastings/nm-operaciya_overlord_kak_byl_otkryt_vtoroj_front/" {
		t.Errorf("запросы: %v", paths)
	}
	// другая книга — запрос, но после паузы
	if _, err := c.Fetch(context.Background(), "Хастингс", "Оверлорд"); err != nil || len(times) != 2 {
		t.Fatalf("%v, запросов %d", err, len(times))
	}
	if gap := times[1].Sub(times[0]); gap < 140*time.Millisecond {
		t.Errorf("пауза между запросами не выдержана: %v", gap)
	}
	if _, err := c.Fetch(context.Background(), "Хастингс", "«»"); err == nil {
		t.Error("пустое название ушло в alib")
	}
}

func TestRublesAndYear(t *testing.T) {
	for in, want := range map[string]int{
		"170 руб.":                   170,
		"1 200 руб.":                 1200,
		"1 200 руб.":                 1200,
		"договорная":                 0,
		"":                           0,
		"руб.":                       0,
		"999999999999999999999 руб.": 0, // не влезает в число — не цена
	} {
		if got := rubles(in); got != want {
			t.Errorf("rubles(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]int{
		"Москва. Прогресс. 1989г. 470 с.":     1989,
		"М. Детская литература 1947 г. 96 с.": 1947,
		"Л. 1986г. Переиздание 2001г.":        1986, // первый — год издания
		"М. Наука. 470 с. Тираж 50000 экз.":   0,
		"Твердый переплет, 1200 г вес":        0, // граммы, а не год
		"Вес 1600 г. М. Наука 1971г.":         1971,
		"Масса: 2000 г. Спб. 2012г.":          2012,
		"Спб. 2012г.":                         2012,
	} {
		if got := year(in); got != want {
			t.Errorf("year(%q) = %d, want %d", in, got, want)
		}
	}
}

// На настоящих страницах: у каждого предложения с ценой цена разобрана, а год есть у
// большинства — продавцы почти всегда пишут его в описании.
func TestOffersHavePriceAndYear(t *testing.T) {
	for _, name := range []string{"hastings.html", "kulikovskij.html", "chukovskij.html"} {
		p := Parse(load(t, name), "u")
		withYear := 0
		for _, o := range p.Offers {
			if o.Price != "" && o.Rub == 0 {
				t.Errorf("%s: цена %q не разобрана", name, o.Price)
			}
			if o.Year != 0 {
				withYear++
			}
		}
		if withYear*10 < len(p.Offers)*8 {
			t.Errorf("%s: год найден у %d из %d предложений", name, withYear, len(p.Offers))
		}
	}
}
