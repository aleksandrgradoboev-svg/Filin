// Package alib читает страницу книги на alib.ru: аннотацию и предложения продавцов
// (год, издательство, состояние, цена).
//
// Поиск alib роботам закрыт (robots.txt: /find), а страницы «автор + название» вида
// alib.ru/au-<фамилия>/nm-<название>/ открыты. Адрес Филин собирает сам по правилу
// транслитерации alib, так что поиск не нужен. alib банит за частые обращения, поэтому —
// только по действию человека, одна-две страницы на книгу, с паузой между запросами.
package alib

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// UserAgent — честно о том, кто и зачем приходит.
const UserAgent = "Filin/0.5 (domashniy katalog knig; po zaprosu cheloveka)"

// Delay — пауза между запросами к alib.
const Delay = 5 * time.Second

// Транслитерация alib, выведенная из карты сайта (tsiteau*.xml) и проверенная на 10 книгах:
// ь → nmn, щ → w, э → q, ъ выпадает.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z",
	'и': "i", 'й': "j", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r",
	'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "h", 'ц': "c", 'ч': "ch", 'ш': "sh", 'щ': "w",
	'ъ': "", 'ы': "y", 'ь': "nmn", 'э': "q", 'ю': "yu", 'я': "ya",
}

// Слова, которые alib выбрасывает из адреса. «в», «и», «по», «к» видны в карте сайта
// («000_kilometrov_boyah_pohodah» — «… километров в боях и походах», «putnmn_zvezdam» —
// «Путь к звездам»); остальные — по тому же образцу, не проверены.
var stop = map[string]bool{"в": true, "и": true, "по": true, "к": true, "на": true, "о": true, "об": true, "у": true}

var reWord = regexp.MustCompile(`[0-9a-zа-яё]+`)

// Slug — часть адреса alib из фамилии или названия.
func Slug(s string) string {
	var parts []string
	for _, w := range reWord.FindAllString(strings.ToLower(s), -1) {
		if stop[w] {
			continue
		}
		var b strings.Builder
		for _, r := range w {
			if t, ok := translit[r]; ok {
				b.WriteString(t)
			} else {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			parts = append(parts, b.String())
		}
	}
	return strings.Join(parts, "_")
}

// PageURL — адрес страницы «автор + название». Автор — фамилия; пусто — книга без автора.
func PageURL(surname, title string) string {
	return "https://www.alib.ru/au-" + Slug(surname) + "/nm-" + Slug(title) + "/"
}

// Offer — предложение продавца.
type Offer struct {
	Seller    string // «BS-Pegas Кунгур.»
	Desc      string // «Москва. Прогресс. 1989г. 470 с. Твердый переплет…»
	Condition string // «хорошее, штамп»
	Price     string // «170 руб.»
	Rub       int    // цена числом: 170; 0 — не разобрана
	Year      int    // год издания из описания: «1989г.» → 1989; 0 — не нашёлся
	URL       string
}

var (
	rePrice = regexp.MustCompile(`^(\d[\d\s\x{00A0}]*)\s*руб`)
	// год — четыре цифры перед «г»: «Прогресс. 1989г.», «1947 г.». Первый такой: дальше
	// в описании бывают годы перепечатки и даты из истории книги
	reYear = regexp.MustCompile(`(?:^|\D)(1[5-9]\d\d|20\d\d)\s?г`)
)

// rubles — цена предложения числом: «170 руб.», «1 200 руб.».
func rubles(price string) int {
	m := rePrice.FindStringSubmatch(price)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, m[1]))
	if err != nil {
		return 0
	}
	return n
}

// reWeight — перед числом стоит вес: «Вес 1600 г.» — это граммы, а не год.
var reWeight = regexp.MustCompile(`(?i)(вес|масса)\W{0,3}$`)

// year — год издания из описания продавца: первый «NNNNг.», который не вес.
func year(desc string) int {
	for _, m := range reYear.FindAllStringSubmatchIndex(desc, -1) {
		if reWeight.MatchString(desc[:m[2]]) {
			continue
		}
		y, _ := strconv.Atoi(desc[m[2]:m[3]])
		return y
	}
	return 0
}

// Page — что на странице книги.
type Page struct {
	URL        string
	Heading    string // как alib называет книгу: «Хастингс Макс. Операция Оверлорд. …»
	Annotation string // одна на страницу; к какому из изданий относится, alib не пишет
	Offers     []Offer
}

// Found — есть ли такая книга на alib.
func (p Page) Found() bool { return p.Heading != "" || len(p.Offers) > 0 }

var (
	reHead  = regexp.MustCompile(`(?s)<hr><p><b>(.*?)</b>\s*(?:<br>(.*?))?</p>`)
	reRow   = regexp.MustCompile(`(?s)<tr valign="top"[^>]*>\s*<td align="center">\d+</td>\s*<td align="left">(.*?)</td>\s*<td align="center">(.*?)</td>`)
	reBuy   = regexp.MustCompile(`(?s)<a href="([^"]+)"[^>]*>\s*Купить за\s*(.*?)</a>`)
	reTags  = regexp.MustCompile(`<[^>]*>`)
	rePhoto = regexp.MustCompile(`(?s)<a [^>]*>фото</a>`)
)

func clean(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(html.UnescapeString(s), "``", "\"")
	return strings.Join(strings.Fields(s), " ")
}

// Parse разбирает страницу alib, уже перекодированную в UTF-8.
func Parse(page, url string) Page {
	p := Page{URL: url}
	if m := reHead.FindStringSubmatch(page); m != nil {
		p.Heading, p.Annotation = clean(m[1]), clean(m[2])
	}
	for _, m := range reRow.FindAllStringSubmatch(page, -1) {
		cell := strings.TrimSpace(rePhoto.ReplaceAllString(m[1], ""))
		parts := strings.Split(cell, "<br>")
		o := Offer{}
		// обычно первая строка — «<b>BS-продавец</b> Город.»; у части продавцов её нет,
		// и имя вклеено прямо в описание («Orion М. Детская литература 1985г.…»)
		if strings.HasPrefix(cell, "<b>") {
			o.Seller, parts = clean(parts[0]), parts[1:]
		}
		for _, part := range parts {
			t := clean(part)
			if c, ok := strings.CutPrefix(t, "Состояние:"); ok {
				o.Condition = strings.TrimSpace(c)
			} else if t != "" {
				o.Desc = strings.TrimSpace(o.Desc + " " + t)
			}
		}
		if b := reBuy.FindStringSubmatch(m[2]); b != nil {
			o.URL, o.Price = b[1], clean(b[2])
			o.Rub = rubles(o.Price)
		}
		o.Year = year(o.Desc)
		p.Offers = append(p.Offers, o)
	}
	return p
}

// Client читает страницы alib. Один клиент на сервер: пауза общая для всех запросов.
type Client struct {
	HTTP  *http.Client
	Base  string        // для тестов; пусто — https://www.alib.ru
	Delay time.Duration // для тестов; 0 — Delay

	mu   sync.Mutex
	last time.Time

	cacheMu sync.Mutex
	cache   map[string]cached // адрес → страница: повторное открытие и «назад» не ходят в alib
}

type cached struct {
	page Page
	at   time.Time
}

// CacheTTL — сколько помнить прочитанную страницу. Предложения на alib загружаются
// дважды в день, так что полчаса ничего не теряют.
const CacheTTL = 30 * time.Minute

const cacheMax = 200

// Peek — страница из памяти, без обращения к alib.
func (c *Client) Peek(url string) (Page, bool) { return c.cached(url) }

func (c *Client) cached(url string) (Page, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	e, ok := c.cache[url]
	if !ok || time.Since(e.at) > CacheTTL {
		return Page{}, false
	}
	return e.page, true
}

func (c *Client) remember(url string, p Page) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.cache == nil || len(c.cache) >= cacheMax {
		c.cache = map[string]cached{} // память домашнего сервера дороже пары повторных запросов
	}
	c.cache[url] = cached{p, time.Now()}
}

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.Delay
	if d == 0 {
		d = Delay
	}
	if left := d - time.Since(c.last); !c.last.IsZero() && left > 0 {
		t := time.NewTimer(left)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	c.last = time.Now()
	return nil
}

// Fetch читает страницу «фамилия + название». Нет такой книги — Page без Found, не ошибка.
func (c *Client) Fetch(ctx context.Context, surname, title string) (Page, error) {
	if Slug(title) == "" {
		return Page{}, fmt.Errorf("для alib нужно название книги")
	}
	url := PageURL(surname, title)
	if p, ok := c.cached(url); ok {
		return p, nil
	}
	if err := c.wait(ctx); err != nil {
		return Page{}, err
	}
	addr := url
	if c.Base != "" {
		addr = c.Base + strings.TrimPrefix(url, "https://www.alib.ru")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return Page{}, fmt.Errorf("alib не ответил: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 502 у alib бывает разовым — человеку стоит просто повторить
		return Page{}, fmt.Errorf("alib ответил %d — попробуйте ещё раз через минуту", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Page{}, err
	}
	text, err := charmap.Windows1251.NewDecoder().Bytes(b)
	if err != nil {
		return Page{}, err
	}
	p := Parse(string(text), url)
	c.remember(url, p)
	return p, nil
}

// Surname — фамилия для адреса alib из имени, как оно записано в Филине. Из LiveLib имена
// идут «Имя Фамилия», из РГБ и руками — чаще «Фамилия Имя Отчество»; инициалы с точкой
// не фамилия. Угадывание — человек поправит в форме.
func Surname(name string, givenFirst bool) string {
	var words []string
	for _, w := range strings.Fields(name) {
		if !strings.Contains(w, ".") {
			words = append(words, w)
		}
	}
	switch {
	case len(words) == 0:
		return ""
	case givenFirst:
		return words[len(words)-1]
	default:
		return words[0]
	}
}

var reTitleCut = regexp.MustCompile(`\s*(?:[:(\[/]|\s—\s|\s-\s).*$`)

// ShortTitle — название, как его заводят на alib: без подзаголовка после двоеточия,
// без скобок «(сборник)» и «/ пер. с англ.».
func ShortTitle(title string) string {
	if s := strings.TrimSpace(reTitleCut.ReplaceAllString(title, "")); Slug(s) != "" {
		return s
	}
	return strings.TrimSpace(title) // «(Не)интересная история» — резать нечего, иначе пусто
}
