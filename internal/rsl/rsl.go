// Package rsl читает библиографическую запись Российской государственной библиотеки
// (search.rsl.ru/ru/record/<номер>) и переводит её в карточку издания Филина.
//
// Ищет человек — в приложении или браузере, на сайте РГБ. Филин только читает запись,
// которую тот выбрал: robots.txt РГБ закрывает роботам всё, кроме страниц записей
// (/ru/record), и Филин в эти рамки укладывается — поиск роботом здесь не делается.
package rsl

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Field — поле MARC: индикаторы и подполя по порядку.
type Field struct {
	Ind  string
	Subs [][2]string // код подполя, значение
}

// Sub — первое подполе с кодом c.
func (f Field) Sub(c string) string {
	for _, s := range f.Subs {
		if s[0] == c {
			return s[1]
		}
	}
	return ""
}

// Record — запись: тег → поля (повторяющиеся, например несколько 020 или 700).
type Record map[string][]Field

func (r Record) first(tag string) (Field, bool) {
	if fs := r[tag]; len(fs) > 0 {
		return fs[0], true
	}
	return Field{}, false
}

// ErrNoRecord — на странице нет записи MARC: не та страница или РГБ сменила разметку.
var ErrNoRecord = errors.New("на странице РГБ не найдена запись MARC — не та страница или сайт сменил разметку")

var (
	reIDInURL = regexp.MustCompile(`(?:^|/record/)(\d{8,14})(?:$|[/?#])`)
	reMarc    = regexp.MustCompile(`(?s)<div id="marc-rec"[^>]*>(.*?)</table>`)
	reRow     = regexp.MustCompile(`(?s)<tr[^>]*>\s*<td[^>]*>(?:<strong>)?\s*([0-9A-Z]{3})\s*(?:</strong>)?</td>\s*<td[^>]*>(.*?)</td>\s*</tr>`)
	reInd     = regexp.MustCompile(`^\s*<strong>([^<]{0,2})</strong>`)
	reSub     = regexp.MustCompile(`<strong>\$([0-9a-z])</strong>(?:&nbsp;|\s)*`)
)

// RecordID достаёт номер записи из ссылки (…/ru/record/01015228461) или берёт номер как есть.
func RecordID(s string) (string, error) {
	m := reIDInURL.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", fmt.Errorf("это не запись РГБ: нужна ссылка вида search.rsl.ru/ru/record/01015228461")
	}
	return m[1], nil
}

// RecordURL — адрес страницы записи.
func RecordURL(id string) string { return "https://search.rsl.ru/ru/record/" + id }

// ParseRecordHTML разбирает таблицу MARC со страницы записи.
func ParseRecordHTML(page string) (Record, error) {
	m := reMarc.FindStringSubmatch(page)
	if m == nil {
		return nil, ErrNoRecord
	}
	rec := Record{}
	for _, row := range reRow.FindAllStringSubmatch(m[1], -1) {
		tag, body := row[1], row[2]
		f := Field{}
		if im := reInd.FindStringSubmatch(body); im != nil {
			f.Ind = im[1]
		}
		locs := reSub.FindAllStringSubmatchIndex(body, -1)
		for i, l := range locs {
			end := len(body)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			code := body[l[2]:l[3]]
			f.Subs = append(f.Subs, [2]string{code, clean(body[l[1]:end])})
		}
		if len(locs) == 0 { // управляющие поля (001, 008) — без подполей
			f.Subs = [][2]string{{"", clean(body)}}
		}
		rec[tag] = append(rec[tag], f)
	}
	if len(rec) == 0 {
		return nil, ErrNoRecord
	}
	return rec, nil
}

var reTags = regexp.MustCompile(`<[^>]+>`)

func clean(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// Client читает записи РГБ. Одна запись — один запрос, по действию человека: прочитанное
// помнится, и сохранение карточки после предпросмотра в РГБ второй раз не ходит.
type Client struct {
	HTTP *http.Client
	Base string // для тестов; пусто — search.rsl.ru

	mu    sync.Mutex
	cache map[string]cachedRecord // номер записи → запись
}

type cachedRecord struct {
	rec Record
	at  time.Time
}

// CacheTTL — сколько помнить прочитанную запись: хватает, чтобы проверить форму и сохранить.
// Каталожная запись меняется редко, но держать её дольше незачем.
const CacheTTL = time.Hour

const cacheMax = 200

// Peek — недавно прочитанная запись из памяти, без обращения к РГБ.
func (c *Client) Peek(id string) (Record, bool) { return c.cached(id) }

func (c *Client) cached(id string) (Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[id]
	if !ok || time.Since(e.at) > CacheTTL {
		return nil, false
	}
	return e.rec, true
}

func (c *Client) remember(id string, r Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil || len(c.cache) >= cacheMax {
		c.cache = map[string]cachedRecord{} // память домашнего сервера дороже пары повторных запросов
	}
	c.cache[id] = cachedRecord{r, time.Now()}
}

// UserAgent — честно о том, кто и зачем приходит.
const UserAgent = "Filin/0.1 (domashniy katalog knig; odna zapis po zaprosu cheloveka)"

// Fetch читает запись по номеру; недавно прочитанная берётся из памяти. Запись не
// меняется после чтения (ToCard только читает), поэтому её можно отдавать повторно.
func (c *Client) Fetch(ctx context.Context, id string) (Record, error) {
	if r, ok := c.cached(id); ok {
		return r, nil
	}
	r, err := c.fetch(ctx, id)
	if err == nil {
		c.remember(id, r)
	}
	return r, err
}

func (c *Client) fetch(ctx context.Context, id string) (Record, error) {
	base := c.Base
	if base == "" {
		base = "https://search.rsl.ru"
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ru/record/"+id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("РГБ не ответила: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("РГБ ответила %d на запись %s", resp.StatusCode, id)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return ParseRecordHTML(string(b))
}

// Card — карточка из записи: издание и годы жизни людей (если в записи есть).
type Card struct {
	Edition catalog.Edition
	Lives   map[string]string // имя → «1837-1904»
	ISBNs   []string          // как в записи; разбирает вызывающий
}

// Роли из MARC: код $4 или слово $e в поле 700.
var roleByCode = map[string]string{
	"aut": "author", "edt": "editor", "trl": "translator", "ill": "illustrator", "com": "compiler",
	"070": "author", "340": "editor", "730": "translator", "440": "illustrator", "220": "compiler",
}

var roleByWord = []struct{ prefix, role string }{
	{"ред", "editor"}, {"пер", "translator"}, {"ил", "illustrator"}, {"худож", "illustrator"}, {"сост", "compiler"},
}

func role(f Field) string {
	if r, ok := roleByCode[strings.ToLower(f.Sub("4"))]; ok {
		return r
	}
	e := strings.ToLower(f.Sub("e"))
	for _, w := range roleByWord {
		if strings.HasPrefix(e, w.prefix) {
			return w.role
		}
	}
	return "author"
}

// «Дубровин, Николай Федорович» → «Дубровин Николай Федорович»: как имена в Филине.
// Точку не трогаем: у инициалов «Бекетова, Е.» она часть имени.
func personName(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), ",;:")
	return strings.Join(strings.Fields(strings.ReplaceAll(s, ",", " ")), " ")
}

var (
	reAge      = regexp.MustCompile(`\[\s*(\d{1,2}\+)\s*\]`)
	reYear     = regexp.MustCompile(`\d{4}`)
	reFirstNum = regexp.MustCompile(`\d+`)
	reCirc     = regexp.MustCompile(`([\d\s]+)\s*экз`)
)

// trimISBD снимает знаки ISBD на стыках подполей: «Название :», «Москва ;», «Эксмо,», «в 20 т. /».
// Финальная точка снимается, только если она не от сокращения: после цифры («Т. 3.») или
// слова длиннее трёх букв («Сочинения.»); «изд.», «к.», «т.» остаются с точкой.
func trimISBD(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), " :;/,=")
	if strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "..") {
		body := strings.TrimSuffix(s, ".")
		word := []rune(body[strings.LastIndexAny(body, " .-")+1:])
		last := []rune(body)
		if len(last) > 0 && (last[len(last)-1] >= '0' && last[len(last)-1] <= '9' || len(word) > 3) {
			s = body
		}
	}
	return strings.TrimSpace(s)
}

// Языки из 041 — коды MARC; переведены самые частые, остальные — как есть.
var langName = map[string]string{
	"rus": "русский", "eng": "английский", "fre": "французский", "fra": "французский", "ger": "немецкий",
	"deu": "немецкий", "spa": "испанский", "ita": "итальянский", "pol": "польский", "cze": "чешский",
	"ces": "чешский", "ukr": "украинский", "bel": "белорусский", "lat": "латинский", "grc": "древнегреческий",
	"gre": "греческий", "jpn": "японский", "chi": "китайский", "swe": "шведский", "nor": "норвежский",
	"dan": "датский", "dut": "нидерландский", "hun": "венгерский", "bul": "болгарский", "srp": "сербский",
}

func lang(code string) string {
	if n, ok := langName[strings.ToLower(code)]; ok {
		return n
	}
	return code
}

// ToCard переводит запись в карточку Филина. Пустые поля записи оставляют поля карточки пустыми.
func ToCard(r Record, id string) Card {
	c := Card{Lives: map[string]string{}}
	e := &c.Edition
	e.Source, e.SourceURL = "rsl", RecordURL(id)

	if f, ok := r.first("100"); ok {
		if n := personName(f.Sub("a")); n != "" {
			e.People = append(e.People, catalog.Person{Name: n, Role: "author"})
			if d := strings.TrimSpace(f.Sub("d")); d != "" {
				c.Lives[n] = strings.ReplaceAll(d, "-", "–")
			}
		}
	}
	for _, f := range r["700"] {
		if n := personName(f.Sub("a")); n != "" {
			e.People = append(e.People, catalog.Person{Name: n, Role: role(f)})
			if d := strings.TrimSpace(f.Sub("d")); d != "" {
				c.Lives[n] = strings.ReplaceAll(d, "-", "–")
			}
		}
	}

	if f, ok := r.first("245"); ok {
		e.Title = trimISBD(f.Sub("a"))
		sub := f.Sub("b")
		if m := reAge.FindStringSubmatch(sub); m != nil {
			e.AgeRating = m[1]
			sub = reAge.ReplaceAllString(sub, "")
		}
		if sub = trimISBD(sub); sub != "" && !strings.EqualFold(sub, e.Title) {
			// подзаголовок, повторяющий вариант заглавия из 246, — не нужен; прочий — к названию
			dup := false
			for _, v := range r["246"] {
				dup = dup || strings.EqualFold(trimISBD(v.Sub("a")), sub)
			}
			if !dup {
				e.Title += ". " + sub
			}
		}
		e.Volume = strings.TrimSpace(strings.Join(strings.Fields(f.Sub("n")+" "+trimISBD(f.Sub("p"))), " "))
	}
	if f, ok := r.first("250"); ok {
		e.EditionNote = trimISBD(f.Sub("a"))
	}
	pub, ok := r.first("260")
	if !ok {
		pub, ok = r.first("264")
	}
	if ok {
		e.City = trimISBD(pub.Sub("a"))
		e.Publisher = trimISBD(pub.Sub("b"))
		if y := reYear.FindString(pub.Sub("c")); y != "" {
			e.Year, _ = strconv.Atoi(y)
		}
	}
	if f, ok := r.first("300"); ok {
		if n := reFirstNum.FindString(f.Sub("a")); n != "" {
			e.Pages, _ = strconv.Atoi(n)
		}
		e.Format = trimISBD(f.Sub("c"))
		if il := trimISBD(f.Sub("b")); il != "" {
			e.Illustrations = il
		}
	}
	for _, tag := range []string{"490", "440", "830"} {
		if f, ok := r.first(tag); ok && e.Series == "" {
			e.Series = strings.Trim(trimISBD(f.Sub("a")), "()")
			e.SeriesNo = strings.TrimSpace(strings.TrimLeft(trimISBD(f.Sub("v")), "№ "))
		}
	}
	for _, f := range r["020"] {
		if a := f.Sub("a"); a != "" {
			c.ISBNs = append(c.ISBNs, a)
		}
		if q := f.Sub("c"); q != "" && e.Circulation == 0 {
			if m := reCirc.FindStringSubmatch(q); m != nil {
				e.Circulation, _ = strconv.Atoi(strings.Join(strings.Fields(m[1]), ""))
				// всё, что до тиража, — цена на книге: «1 р. 20 к., 100000 экз.»
				if price := trimISBD(q[:strings.Index(q, m[0])]); price != "" {
					e.PrintPrice = price
				}
			}
		}
	}
	if f, ok := r.first("084"); ok {
		e.BBK = f.Sub("a")
	}
	if f, ok := r.first("080"); ok {
		e.UDK = f.Sub("a")
	}
	if f, ok := r.first("041"); ok {
		if a := f.Sub("a"); a != "" && a != "rus" {
			e.Language = lang(a)
		}
		if h := f.Sub("h"); h != "" {
			e.OrigLanguage = lang(h)
		}
	}
	if f, ok := r.first("520"); ok {
		e.Annotation = f.Sub("a")
	}
	if f, ok := r.first("505"); ok {
		e.Contents = strings.ReplaceAll(f.Sub("a"), " -- ", "\n")
	}
	return c
}
