package rsl

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Многотомное издание РГБ описывает двумя записями. У тома своя запись — в ней ISBN тома,
// страницы, иллюстрации и содержание, а в заглавии только «Т. 2». Автор, название,
// издательство, формат, тираж, ББК и серия — в записи всего издания, на которую том
// ссылается полем 773 (подполе $w — номер той записи). Без неё карточка тома
// получается безымянной.

var reNineDigits = regexp.MustCompile(`^\d{9}$`)

// WholeID — номер записи всего издания, если rec — запись тома; иначе пусто.
// В 773 $w стоит поле 001 той записи (9 цифр), номер на сайте — с приставкой «01».
func WholeID(rec Record) string {
	f, ok := rec.first("773")
	if !ok {
		return ""
	}
	w := strings.TrimSpace(f.Sub("w"))
	if !reNineDigits.MatchString(w) {
		return ""
	}
	return "01" + w
}

// Card читает запись и собирает карточку. Запись тома дополняется записью всего издания;
// не ответила РГБ на вторую — карточка собирается из ссылки 773 в самой записи тома.
func (c *Client) Card(ctx context.Context, id string) (Card, error) {
	rec, err := c.Fetch(ctx, id)
	if err != nil {
		return Card{}, err
	}
	wid := WholeID(rec)
	if wid == "" {
		return ToCard(rec, id), nil
	}
	whole, err := c.Fetch(ctx, wid)
	if err != nil {
		log.Printf("РГБ: запись издания %s для тома %s не прочитана, карточка из ссылки тома: %v", wid, id, err)
		whole = nil
	}
	return VolumeCard(rec, id, whole), nil
}

// PeekCard — карточка из недавно прочитанных записей, без обращения к РГБ.
func (c *Client) PeekCard(id string) (Card, bool) {
	rec, ok := c.Peek(id)
	if !ok {
		return Card{}, false
	}
	wid := WholeID(rec)
	if wid == "" {
		return ToCard(rec, id), true
	}
	whole, _ := c.Peek(wid)
	return VolumeCard(rec, id, whole), true
}

// VolumeCard — карточка тома: основа из записи всего издания (whole; nil — из 773 тома),
// поверх неё — то, что относится к самому тому. Ссылка карточки — на запись тома.
func VolumeCard(vol Record, id string, whole Record) Card {
	var c Card
	if whole != nil {
		c = ToCard(whole, id)
	} else {
		c = fromLink(vol, id)
	}
	v := ToCard(vol, id)
	e, ve := &c.Edition, v.Edition

	// «Т. 2» из заглавия тома — номер тома, а не название; $n/$p тома — его часть
	e.Volume = strings.Join(nonEmpty(ve.Title, ve.Volume), ". ")
	if e.Volume == "" {
		if f, ok := vol.first("773"); ok {
			e.Volume = trimISBD(f.Sub("g"))
		}
	}
	if ve.Pages != 0 {
		e.Pages = ve.Pages
	}
	pick := func(dst *string, src string) {
		if src != "" {
			*dst = src
		}
	}
	pick(&e.Illustrations, ve.Illustrations)
	pick(&e.Format, ve.Format)
	pick(&e.Contents, ve.Contents)
	pick(&e.Annotation, ve.Annotation)
	pick(&e.BBK, ve.BBK)
	pick(&e.UDK, ve.UDK)
	pick(&e.Language, ve.Language)
	pick(&e.OrigLanguage, ve.OrigLanguage)
	pick(&e.PrintPrice, ve.PrintPrice)
	if ve.Circulation != 0 {
		e.Circulation = ve.Circulation
	}
	if e.Year == 0 {
		e.Year = ve.Year
	}
	// ISBN тома — свой; ISBN комплекта берётся, только если своего нет: иначе он оказался бы
	// у каждого тома, и скан комплекта узнавал бы любой уже заведённый том
	if len(v.ISBNs) > 0 {
		c.ISBNs = v.ISBNs
	}
	// у тома бывают свои люди (переводчик, составитель тома) — к общим, без повторов
	have := map[string]bool{}
	for _, p := range e.People {
		have[p.Name+"/"+p.Role] = true
	}
	for _, p := range ve.People {
		if !have[p.Name+"/"+p.Role] {
			e.People = append(e.People, p)
		}
	}
	// темы тома — к темам издания, без повторов
	e.Subjects = catalog.ParseSubjects(strings.Join(append(append([]string{}, e.Subjects...), ve.Subjects...), "\n"))
	for n, l := range v.Lives {
		c.Lives[n] = l
	}
	return c
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var (
	// «(1872-1947)», «(1975-)», «(род. 1950)» — годы в конце имени
	reLifeInName = regexp.MustCompile(`\s*\(([^()]*\d{4}[^()]*)\)\s*$`)
	// общее обозначение материала после заглавия: «[Текст]», «[Карты]», «[Ноты]»…
	reGMD = regexp.MustCompile(`\s*\[(?i:текст|ноты|карты|изоматериал|электронный ресурс|звукозапись|видеозапись)\]\s*`)
)

// fromLink — карточка всего издания из поля 773 записи тома, когда саму запись издания
// прочитать не удалось: «$a Деникин, Антон Иванович (1872-1947) $t Очерки русской смуты
// [Текст] : в двух томах : [18+] / Деникин Антон Иванович $b 2-е изд. $d Минск : Харвест, 2017».
func fromLink(vol Record, id string) Card {
	c := Card{Lives: map[string]string{}}
	e := &c.Edition
	e.Source, e.SourceURL = "rsl", RecordURL(id)
	f, ok := vol.first("773")
	if !ok {
		return c
	}
	if a := f.Sub("a"); a != "" {
		life := ""
		if m := reLifeInName.FindStringSubmatch(a); m != nil {
			life = strings.ReplaceAll(strings.TrimSpace(m[1]), "-", "–")
			a = a[:len(a)-len(m[0])]
		}
		if n := personName(a); n != "" {
			e.People = append(e.People, catalog.Person{Name: n, Role: "author"})
			if life != "" {
				c.Lives[n] = life
			}
		}
	}
	t := f.Sub("t")
	if i := strings.Index(t, " / "); i >= 0 { // сведения об ответственности — это автор, он уже есть
		t = t[:i]
	}
	t = reGMD.ReplaceAllString(t, " ")
	if m := reAge.FindStringSubmatch(t); m != nil {
		e.AgeRating = m[1]
		t = reAge.ReplaceAllString(t, "")
	}
	parts := strings.Split(t, " : ")
	e.Title = trimISBD(parts[0])
	for _, p := range parts[1:] {
		if p = trimISBD(p); p != "" && !noiseSubtitle(p) {
			e.Title += ". " + p
		}
	}
	e.EditionNote = trimISBD(f.Sub("b"))
	if d := f.Sub("d"); d != "" { // «Минск : Харвест, печ. 2017»
		// без « : » издательства в ссылке нет — город и год не угадываем по запятым
		if place, rest, found := strings.Cut(d, " : "); found {
			e.City = trimISBD(place)
			if i := strings.LastIndex(rest, ","); i >= 0 {
				rest = rest[:i]
			}
			e.Publisher = trimISBD(rest)
		}
		if y := reYear.FindString(d); y != "" {
			e.Year, _ = strconv.Atoi(y)
		}
	}
	return c
}
