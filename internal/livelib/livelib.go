// Package livelib разбирает страницы полок LiveLib, сохранённые человеком вручную
// (Chrome: «Сохранить как → веб-страница, один файл», .mhtml). Сайт запрещает
// автоматический доступ (правила, п. 3.2.11), поэтому только так: никаких запросов к
// livelib.ru отсюда нет — обложки берутся из самого файла, куда их положил браузер.
package livelib

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"regexp"
	"strings"
)

// Book — книга с полки, как её показывает LiveLib в режиме плитки.
type Book struct {
	ID      string // id издания на LiveLib, из адреса /book/<id>-…
	URL     string
	Title   string
	Authors []string
	Mine    bool   // значок «Мои книги»: книга стоит у человека дома
	Cover   []byte // картинка обложки из файла; nil — в файле её нет
}

// ErrNoBooks — в файле нет ни одной книги в разметке полки.
var ErrNoBooks = errors.New("в файле не найдено книг полки LiveLib — сохранена не та страница или LiveLib сменил разметку")

// ParseMHTML разбирает .mhtml со страницей полки.
func ParseMHTML(data []byte) ([]Book, error) {
	page, res, err := splitMHTML(data)
	if err != nil {
		return nil, err
	}
	books := parsePage(page)
	if len(books) == 0 {
		return nil, ErrNoBooks
	}
	for i := range books {
		books[i].Cover = res[books[i].coverURL]
	}
	out := make([]Book, len(books))
	for i, b := range books {
		out[i] = b.Book
	}
	return out, nil
}

// splitMHTML отдаёт HTML страницы и ресурсы по их адресу (Content-Location).
func splitMHTML(data []byte) (page string, res map[string][]byte, err error) {
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(data)))
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return "", nil, errors.New("это не .mhtml: нет заголовков письма")
	}
	mt, params, err := mime.ParseMediaType(hdr.Get("Content-Type"))
	if err != nil || mt != "multipart/related" || params["boundary"] == "" {
		return "", nil, errors.New("это не .mhtml: нет multipart/related")
	}
	mr := multipart.NewReader(tp.R, params["boundary"])
	res = map[string][]byte{}
	for {
		p, err := mr.NextRawPart() // сырой: кодировку снимаем сами, и base64 тоже
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		var r io.Reader = p
		switch strings.ToLower(p.Header.Get("Content-Transfer-Encoding")) {
		case "quoted-printable":
			r = quotedprintable.NewReader(p)
		case "base64":
			r = base64.NewDecoder(base64.StdEncoding, newlineStripper{p})
		}
		body, err := io.ReadAll(r)
		if err != nil {
			return "", nil, err
		}
		ct := p.Header.Get("Content-Type")
		if page == "" && strings.HasPrefix(ct, "text/html") {
			page = string(body)
			continue
		}
		if loc := p.Header.Get("Content-Location"); loc != "" {
			res[loc] = body
		}
	}
	if page == "" {
		return "", nil, errors.New("в .mhtml нет HTML-страницы")
	}
	return page, res, nil
}

// newlineStripper убирает переводы строк из base64 — стандартный декодер их не терпит.
type newlineStripper struct{ r io.Reader }

func (n newlineStripper) Read(p []byte) (int, error) {
	k, err := n.r.Read(p)
	j := 0
	for _, c := range p[:k] {
		if c != '\r' && c != '\n' {
			p[j] = c
			j++
		}
	}
	return j, err
}

type parsed struct {
	Book
	coverURL string
}

// Разметка плитки LiveLib: каждая книга — блок brow book-item-manage; внутри обложка
// со ссылкой на книгу, значок «Мои книги», название и ссылки на авторов.
var (
	reBookURL  = regexp.MustCompile(`<a class="swiper-book-name" href="(https://www\.livelib\.ru/book/(\d+)[^"]*)" title="([^"]*)"[^>]*>([^<]*)</a>`)
	reMoreAuth = regexp.MustCompile(`^Ещ[её] \d+$`)
	reCover    = regexp.MustCompile(`class="cover-rounded" src="([^"]+)"`)
	reAuthors  = regexp.MustCompile(`(?s)<div class="swiper-author">(.*?)</div>`)
	reAuthor   = regexp.MustCompile(`<a [^>]*>([^<]+)</a>`)
	reMineMark = regexp.MustCompile(`class="bg-book-text ub"><span>Мои книги</span>`)
)

func parsePage(page string) []parsed {
	var out []parsed
	seen := map[string]bool{}
	for _, block := range strings.Split(page, `<div class="brow book-item-manage"`)[1:] {
		m := reBookURL.FindStringSubmatch(block)
		if m == nil || seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		b := parsed{Book: Book{
			URL:  m[1],
			ID:   m[2],
			Mine: reMineMark.MatchString(block),
		}}
		if c := reCover.FindStringSubmatch(block); c != nil {
			b.coverURL = html.UnescapeString(c[1])
		}
		if a := reAuthors.FindStringSubmatch(block); a != nil {
			for _, n := range reAuthor.FindAllStringSubmatch(a[1], -1) {
				// плитка обрезает длинный список: «Иванов И., Пет...», «Ещё 1» — такие
				// имена не берём, половина имени хуже никакого
				name := clean(n[1])
				if name != "" && !strings.HasSuffix(name, "...") && !reMoreAuth.MatchString(name) {
					b.Authors = append(b.Authors, name)
				}
			}
		}
		b.Title = fullTitle(clean(m[3]), clean(m[4]), b.Authors)
		out = append(out, b)
	}
	return out
}

// fullTitle — название без обрезки. Текст ссылки плитка обрезает («Мировой кризис.
// Часть 3 (ко...»), а в атрибуте title оно целиком — у книги с одним автором в виде
// «Автор - Название». Приставку снимаем, только если это ровно имя единственного автора:
// в самих названиях « - » тоже бывает.
func fullTitle(attr, text string, authors []string) string {
	if attr == "" {
		return text
	}
	if len(authors) == 1 {
		if t, ok := strings.CutPrefix(attr, authors[0]+" - "); ok {
			return t
		}
	}
	return attr
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}
