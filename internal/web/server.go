// Package web — веб-приложение каталога: список, карточка, добавление, правка, сканер ISBN.
// Страницы собираются на сервере из шаблонов; JavaScript — только у сканера и подсказок жанров.
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/alib"
	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
	"github.com/aleksandrgradoboev-svg/filin/internal/ocr"
	"github.com/aleksandrgradoboev-svg/filin/internal/rsl"
)

//go:embed tmpl static
var files embed.FS

const pageSize = 60

// maxUpload — предел тела формы: фото с телефона весит до 10 МБ, с запасом.
const maxUpload = 25 << 20

type server struct {
	st     *catalog.Store
	covers cover.Dir
	pages  map[string]*template.Template
	pair   pairing // код привязки телефона
	appDir string  // выложенное приложение для телефона: рядом с обложками, <данные>/app
	rsl    rsl.Client
	alib   *alib.Client // один на сервер: пауза между запросами к alib общая
	ocr    ocr.Engine
	probes ocr.Samples // пробы распознавания титулов: <данные>/ocr
	phone  []string    // адреса для телефона в домашней сети: показываются на странице «Телефоны»

	bundledInfo, bundledAPK []byte // приложение, вшитое в выпуск; пусто — только выложенное
}

// New собирает обработчик приложения поверх каталога. covers — каталог, где лежат
// обложки; заводится при первой загрузке.
func New(st *catalog.Store, covers string, opts ...Option) (http.Handler, error) {
	s := &server{st: st, covers: cover.Dir(covers), pages: map[string]*template.Template{},
		appDir: filepath.Join(filepath.Dir(covers), "app"), alib: &alib.Client{}, probes: ocr.Samples(filepath.Join(filepath.Dir(covers), "ocr"))}
	for _, o := range opts {
		o(s)
	}
	funcs := template.FuncMap{
		"people":    peopleGroups,
		"personurl": personURL,
		"roletitle": roleTitle,
		"roles":     func() []roleField { return roleFields },
		"add":       func(a, b int) int { return a + b },
		"shelf":     shelfLabel,
		"cf":        newCopyFieldsData,
		"date":      ruDate,
		"who":       whoURL,
		"days":      daysSince,
		"loanlist":  newLoanListData,
		"rub":       rub,
		"join":      strings.Join,
		"price":     price,
		"grade":     catalog.GradeName,
		"grades":    gradeScale, // шкала сверху вниз, с пояснениями
	}
	for _, p := range []string{"list", "book", "edition_form", "copy_form", "scan", "loans", "borrower", "value", "series", "genres", "person", "people", "devices", "alib"} {
		t, err := template.New("").Funcs(funcs).ParseFS(files, "tmpl/layout.html", "tmpl/copyfields.html", "tmpl/loanlist.html", "tmpl/"+p+".html")
		if err != nil {
			return nil, err
		}
		s.pages[p] = t
	}
	static, err := fs.Sub(files, "static")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		http.ServeFileFS(w, r, static, "manifest.webmanifest")
	})
	mux.HandleFunc("GET /cover/{name}", s.cover)
	mux.HandleFunc("GET /{$}", s.list)
	mux.HandleFunc("GET /scan", s.scan)
	mux.HandleFunc("GET /add", s.addForm)
	mux.HandleFunc("POST /add", s.add)
	mux.HandleFunc("GET /book/{id}", s.book)
	mux.HandleFunc("GET /book/{id}/edit", s.editForm)
	mux.HandleFunc("POST /book/{id}/edit", s.edit)
	mux.HandleFunc("GET /book/{id}/alib", s.alibForm)
	mux.HandleFunc("POST /book/{id}/alib", s.alibTake)
	mux.HandleFunc("POST /book/{id}/alib/value", s.alibValue)
	mux.HandleFunc("POST /book/{id}/copy", s.addCopy)
	mux.HandleFunc("GET /copy/{id}/edit", s.copyForm)
	mux.HandleFunc("POST /copy/{id}/edit", s.editCopy)
	mux.HandleFunc("POST /copy/{id}/delete", s.deleteCopy)
	mux.HandleFunc("POST /copy/{id}/lend", s.lend)
	mux.HandleFunc("POST /loan/{id}/return", s.returnLoan)
	mux.HandleFunc("GET /loans", s.loans)
	mux.HandleFunc("GET /loans/who", s.borrower)
	mux.HandleFunc("POST /copy/{id}/value", s.addValuation)
	mux.HandleFunc("POST /valuation/{id}/delete", s.deleteValuation)
	mux.HandleFunc("GET /value", s.value)
	mux.HandleFunc("GET /series/{id}", s.series)
	mux.HandleFunc("GET /genres", s.genres)
	mux.HandleFunc("GET /person", s.person)
	mux.HandleFunc("POST /person", s.savePerson)
	mux.HandleFunc("GET /people", s.people)
	mux.HandleFunc("GET /devices", s.devices)
	mux.HandleFunc("POST /devices/code", s.deviceCode)
	mux.HandleFunc("POST /devices/{id}/revoke", s.revokeDevice)
	mux.HandleFunc("POST /api/pair", s.apiPair)
	mux.HandleFunc("GET /api/me", s.apiMe)
	mux.HandleFunc("GET /api/isbn/{isbn}", s.apiISBN)
	mux.HandleFunc("POST /api/books", s.apiAddBook)
	mux.HandleFunc("POST /api/books/{id}/copies", s.apiAddCopy)
	mux.HandleFunc("PUT /api/books/{id}/cover", s.apiCover)
	mux.HandleFunc("GET /api/shelves", s.apiShelves)
	mux.HandleFunc("GET /api/app", s.apiApp)
	mux.HandleFunc("GET /api/rsl/{id}", s.apiRSL)
	mux.HandleFunc("POST /api/title", s.apiTitle)
	mux.HandleFunc("GET /app.apk", s.appAPK)
	mux.HandleFunc("GET /backup", s.backup)
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, PingBody) })
	return guardHost(sameOrigin(s.access(mux))), nil
}

// guardHost закрывает DNS rebinding: чужой сайт перепривязывает своё имя на 127.0.0.1 —
// и браузер шлёт ему запросы «изнутри» компьютера, где Филин считает их хозяйскими
// (так скачивалась бы вся резервная копия). Филина открывают только по IP-адресу или
// localhost — запрос с любым другим именем в Host отклоняется. Заодно — заголовки против
// показа страниц Филина во фрейме чужого сайта (подмена кликов) и угадывания типа.
func guardHost(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if hh, _, err := net.SplitHostPort(host); err == nil {
			host = hh
		}
		host = strings.Trim(host, "[]")
		if net.ParseIP(host) == nil && !strings.EqualFold(host, "localhost") {
			http.Error(w, "Филин открывается по адресу компьютера (127.0.0.1 или 192.168.…), а не по имени сайта",
				http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.ServeHTTP(w, r)
	})
}

// sameOrigin не пускает запись с чужих страниц: без входа по паролю любой сайт, открытый
// в том же браузере, мог бы отправить форму на localhost.
func sameOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					http.Error(w, "запрос с чужой страницы", http.StatusForbidden)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}

func (s *server) render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// страницы — всегда свежие: без этого приложение на телефоне по «назад» показывает
	// каталог из своей памяти, без обложки, только что снятой в карточке
	w.Header().Set("Cache-Control", "no-store")
	if err := s.pages[page].ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("%s: %v", page, err)
	}
}

func (s *server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrNotFound) {
		http.NotFound(w, nil)
		return
	}
	if catalog.IsInput(err) {
		http.Error(w, err.Error()+". Вернитесь назад и поправьте.", http.StatusBadRequest)
		return
	}
	log.Print(err)
	http.Error(w, "ошибка сервера: "+err.Error(), http.StatusInternalServerError)
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, catalog.ErrNotFound
	}
	return id, nil
}

func shelfLabel(room, bookcase, shelf string) string {
	var parts []string
	for _, p := range []string{room, bookcase, shelf} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " → ")
}

// --- список ---

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	view := "tiles"
	if r.URL.Query().Get("view") == "list" {
		view = "list"
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	if page < 1 {
		page = 1
	}
	g, _ := strconv.ParseInt(r.URL.Query().Get("g"), 10, 64)
	genre := ""
	if g != 0 {
		var err error
		if genre, err = s.st.GenreName(g); err != nil {
			s.fail(w, err)
			return
		}
	}
	rows, total, err := s.st.SearchIn(q, g, pageSize, (page-1)*pageSize)
	if err != nil {
		s.fail(w, err)
		return
	}
	stats, err := s.st.Stats()
	if err != nil {
		s.fail(w, err)
		return
	}
	// ссылки собираются здесь: в шаблоне html/template экранировал бы «&» и «=» готовой строки
	link := func(view string, g int64, page int) template.URL {
		v := url.Values{}
		if q != "" {
			v.Set("q", q)
		}
		if view == "list" {
			v.Set("view", "list")
		}
		if g != 0 {
			v.Set("g", strconv.FormatInt(g, 10))
		}
		if page > 1 {
			v.Set("p", strconv.Itoa(page))
		}
		if len(v) == 0 {
			return "/"
		}
		return template.URL("/?" + v.Encode())
	}
	s.render(w, "list", struct {
		Title                     string
		Q, View, Genre            string
		GenreID                   int64
		Rows                      []catalog.Row
		Total, Page               int
		HasNext                   bool
		Stats                     catalog.Stats
		Prev, Next, Tiles, List   template.URL
		NoGenre, NoQuery, AllBook template.URL
	}{
		Title: "Каталог", Q: q, View: view, Genre: genre, GenreID: g, Rows: rows, Total: total, Page: page,
		HasNext: page*pageSize < total, Stats: stats,
		Prev: link(view, g, page-1), Next: link(view, g, page+1), Tiles: link("tiles", g, 1), List: link("list", g, 1),
		NoGenre: link(view, 0, 1), AllBook: "/",
	})
}

// --- карточка ---

type bookPage struct {
	Title     string
	E         catalog.Edition
	Copies    []bookCopy
	Shelves   []catalog.ShelfRow
	Borrowers []string
	Today     string
	Added     bool
	Warn      string // что не сохранилось при добавлении: cover, copy
	OpenCopy  bool   // пришли со сканера к уже заведённому изданию — сразу форма экземпляра
	Blank     copyFields
	LendErr   string
	LendCopy  int64 // экземпляр, на выдаче которого ошибка: его форма открыта
	ValueErr  string
	ValueCopy int64 // то же для формы оценки
	Genres    []catalog.Genre
}

// bookCopy — экземпляр с историей выдач.
type bookCopy struct {
	catalog.ShelvedCopy
	PastLoans  []catalog.Loan      // возвращённые выдачи; History — уже «история экземпляра»
	Valuations []catalog.Valuation // все оценки, новые сверху; первая — действующая
}

func (s *server) book(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	q := r.URL.Query()
	s.renderBook(w, bookPage{E: catalog.Edition{ID: id}, Added: q.Has("added"), OpenCopy: q.Has("copy"),
		Warn: q.Get("warn")})
}

// renderBook дочитывает в p всё по изданию p.E.ID и рисует карточку.
func (s *server) renderBook(w http.ResponseWriter, p bookPage) {
	var err error
	if p.E, err = s.st.Edition(p.E.ID); err != nil {
		s.fail(w, err)
		return
	}
	p.Title, p.Today = p.E.Title, catalog.Today()
	if p.Genres, err = s.st.EditionGenres(p.E.ID); err != nil {
		s.fail(w, err)
		return
	}
	cs, err := s.st.Copies(p.E.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, c := range cs {
		all, err := s.st.CopyLoans(c.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		bc := bookCopy{ShelvedCopy: c}
		if bc.Valuations, err = s.st.CopyValuations(c.ID); err != nil {
			s.fail(w, err)
			return
		}
		for _, l := range all {
			if l.ReturnedOn != "" {
				bc.PastLoans = append(bc.PastLoans, l)
			}
		}
		p.Copies = append(p.Copies, bc)
	}
	if p.Shelves, err = s.st.Shelves(); err != nil {
		s.fail(w, err)
		return
	}
	if p.Borrowers, err = s.st.Borrowers(); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "book", p)
}

// --- форма издания: добавление и правка ---

type editionForm struct {
	Title    string
	Action   string
	IsNew    bool
	E        catalog.Edition
	People   map[string]string // поле роли → имена через запятую
	ISBNs    string
	Year     string
	Copy     copyFields
	WithCopy bool
	Errors   []string
	Matches  []catalog.Edition // издания с тем же ISBN — возможно, книга уже есть
	Shelves  []catalog.ShelfRow
	// названия серий для подсказок
	SeriesNames []string
	Genres      string          // как в поле: через запятую
	AllGenres   []catalog.Genre // подсказки
	RSL         string          // запись РГБ, из которой заполнена форма
	Fill        *fillReport     // правка: что запись РГБ добавила к уже заведённой книге

	coverJPEG   []byte // загруженная и уже обработанная обложка
	removeCover bool
}

type copyFields struct {
	Room, Bookcase, Shelf, Condition, Notes string
	NoLend, Cherished, Read                 bool
	Grade                                   int
}

func (s *server) addForm(w http.ResponseWriter, r *http.Request) {
	f := editionForm{Title: "Новая книга", Action: "/add", IsNew: true, WithCopy: true,
		ISBNs: r.URL.Query().Get("isbn")}
	// «Заполнить из РГБ»: человек нашёл запись сам и вставил ссылку — форма приходит заполненной
	if ref := strings.TrimSpace(r.URL.Query().Get("rsl")); ref != "" {
		c, err := s.rslCard(r, ref)
		if err != nil {
			f.Errors = append(f.Errors, "РГБ: "+err.Error())
		} else {
			f.E, f.RSL = c.Edition, c.Edition.SourceURL
			f.People = peopleByRole(c.Edition.People)
			if c.Edition.Year != 0 {
				f.Year = strconv.Itoa(c.Edition.Year)
			}
			if f.ISBNs == "" {
				f.ISBNs = isbnList(c.Edition.ISBNs)
			}
		}
	}
	if f.ISBNs != "" {
		var err error
		if f.Matches, err = s.st.EditionsByISBN(f.ISBNs); err != nil && !errors.Is(err, isbn.ErrNotISBN) {
			s.fail(w, err)
			return
		}
	}
	s.renderEditionForm(w, f)
}

func (s *server) renderEditionForm(w http.ResponseWriter, f editionForm) {
	var err error
	if f.AllGenres, err = s.st.Genres(); err != nil {
		s.fail(w, err)
		return
	}
	if f.SeriesNames, err = s.st.SeriesNames(); err != nil {
		s.fail(w, err)
		return
	}
	if f.Shelves, err = s.st.Shelves(); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "edition_form", f)
}

// readCover достаёт обложку из формы и сразу её обрабатывает: не картинка — ошибка формы
// до того, как издание сохранено.
func readCover(r *http.Request, f *editionForm) {
	f.removeCover = r.FormValue("remove_cover") != ""
	file, _, err := r.FormFile("cover")
	if errors.Is(err, http.ErrMissingFile) || errors.Is(err, http.ErrNotMultipart) {
		return
	}
	if err != nil {
		f.Errors = append(f.Errors, "обложка не загрузилась: "+err.Error())
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		f.Errors = append(f.Errors, "обложка не загрузилась: "+err.Error())
		return
	}
	if len(data) == 0 {
		return
	}
	if f.coverJPEG, err = cover.Process(data); err != nil {
		f.Errors = append(f.Errors, "обложка: "+err.Error())
	}
}

// saveCover ставит или снимает обложку по данным формы.
func (s *server) saveCover(id int64, f *editionForm) error {
	if f.coverJPEG == nil && !f.removeCover {
		return nil
	}
	return s.covers.Replace(id, f.coverJPEG, func(name string) (string, error) { return s.st.SetCover(id, name) })
}

func (s *server) cover(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !cover.ValidName(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, s.covers.Path(name))
}

// readEdition разбирает поля издания. Неразобранные ISBN и кривой год — ошибки формы:
// форма возвращается человеку с тем, что он ввёл, а не теряет номер молча.
func readEdition(r *http.Request, f *editionForm) {
	f.E.Title = strings.TrimSpace(r.FormValue("title"))
	f.E.Publisher = strings.TrimSpace(r.FormValue("publisher"))
	f.E.City = strings.TrimSpace(r.FormValue("city"))
	f.E.Volume = strings.TrimSpace(r.FormValue("volume"))
	f.E.Notes = strings.TrimSpace(r.FormValue("notes"))
	f.E.Series = strings.TrimSpace(r.FormValue("series"))
	f.E.SeriesNo = strings.TrimSpace(r.FormValue("series_no"))
	f.Genres = r.FormValue("genres")
	f.E.Genres = catalog.ParseGenres(f.Genres)
	for _, k := range []struct {
		name string
		to   *string
	}{
		{"bbk", &f.E.BBK}, {"udk", &f.E.UDK}, {"author_sign", &f.E.AuthorSign},
		{"cover_type", &f.E.CoverType}, {"format", &f.E.Format},
		{"orig_title", &f.E.OrigTitle}, {"orig_language", &f.E.OrigLanguage}, {"language", &f.E.Language},
		{"edition_note", &f.E.EditionNote}, {"print_price", &f.E.PrintPrice}, {"printer", &f.E.Printer},
		{"age_rating", &f.E.AgeRating}, {"paper", &f.E.Paper}, {"design", &f.E.Design},
		{"illustrations", &f.E.Illustrations}, {"dimensions", &f.E.Dimensions},
	} {
		*k.to = strings.Join(strings.Fields(r.FormValue(k.name)), " ")
	}
	// многострочные — переводы строк сохраняются, лишние пробелы по краям — нет
	f.E.Annotation = strings.TrimSpace(r.FormValue("annotation"))
	f.E.Contents = strings.TrimSpace(r.FormValue("contents"))
	f.E.Pages = formInt(r, "pages", "страниц", &f.Errors)
	f.E.Circulation = formInt(r, "circulation", "тираж", &f.Errors)
	f.E.Weight = formInt(r, "weight", "вес", &f.Errors)
	if f.E.SeriesNo != "" && f.E.Series == "" {
		f.Errors = append(f.Errors, "номер в серии указан, а серия — нет")
	}
	f.E.People = readPeople(r)
	f.People = map[string]string{}
	for _, rf := range roleFields {
		f.People[rf.Name] = r.FormValue(rf.Name)
	}
	f.ISBNs = r.FormValue("isbns")
	good, bad := catalog.ParseISBNs(f.ISBNs)
	f.E.ISBNs = good
	if len(bad) > 0 {
		f.Errors = append(f.Errors, "не разобраны ISBN: "+strings.Join(bad, ", "))
	}
	f.Year = strings.TrimSpace(r.FormValue("year"))
	if f.Year != "" {
		y, err := strconv.Atoi(f.Year)
		if err != nil || y < 1400 || y > 2100 {
			f.Errors = append(f.Errors, "год — четыре цифры, например 1987")
		}
		f.E.Year = y
	}
	if f.E.Title == "" {
		f.Errors = append(f.Errors, "нужно название")
	}
}

func readCopy(r *http.Request) copyFields {
	g, err := strconv.Atoi(r.FormValue("grade"))
	if err != nil || catalog.GradeName(g) == "" {
		g = 0 // «не оценено» или что-то вне шкалы
	}
	return copyFields{
		Room:      strings.TrimSpace(r.FormValue("room")),
		Bookcase:  strings.TrimSpace(r.FormValue("bookcase")),
		Shelf:     strings.TrimSpace(r.FormValue("shelf")),
		Condition: strings.TrimSpace(r.FormValue("condition")),
		Notes:     strings.TrimSpace(r.FormValue("copy_notes")),
		NoLend:    r.FormValue("no_lend") != "",
		Cherished: r.FormValue("cherished") != "",
		Read:      r.FormValue("is_read") != "",
		Grade:     g,
	}
}

// shelfID заводит полку по трём полям; пустые все три — полка не указана.
func (s *server) shelfID(c copyFields) (int64, error) {
	if c.Room == "" && c.Bookcase == "" && c.Shelf == "" {
		return 0, nil
	}
	return s.st.Shelf(c.Room, c.Bookcase, c.Shelf)
}

func (s *server) saveCopy(editionID int64, c copyFields, cp *catalog.Copy) error {
	sh, err := s.shelfID(c)
	if err != nil {
		return err
	}
	cp.EditionID, cp.ShelfID, cp.Condition, cp.Notes, cp.NoLend = editionID, sh, c.Condition, c.Notes, c.NoLend
	cp.Grade, cp.Cherished, cp.Read = c.Grade, c.Cherished, c.Read
	if cp.ID == 0 {
		_, err = s.st.AddCopy(cp)
		return err
	}
	return s.st.UpdateCopy(cp)
}

// parseForm разбирает форму заранее: иначе переполнение предела тела молча
// превращается в пустые поля и ошибку «нужно название».
func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	err := r.ParseMultipartForm(maxUpload)
	if errors.Is(err, http.ErrNotMultipart) {
		err = r.ParseForm()
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return fmt.Errorf("файл больше %d МБ — уменьшите фото", maxUpload>>20)
	}
	return err
}

func (s *server) add(w http.ResponseWriter, r *http.Request) {
	f := editionForm{Title: "Новая книга", Action: "/add", IsNew: true, WithCopy: true}
	if err := parseForm(w, r); err != nil {
		f.Errors = []string{err.Error()}
		s.renderEditionForm(w, f)
		return
	}
	readEdition(r, &f)
	readCover(r, &f)
	f.Copy = readCopy(r)
	f.WithCopy = r.FormValue("with_copy") != ""
	// карточка заполнялась из РГБ — запомнить источник; годы жизни авторов взять из записи
	var lives map[string]string
	if f.RSL = r.FormValue("rsl"); f.RSL != "" {
		if id, err := rsl.RecordID(f.RSL); err == nil {
			f.E.Source, f.E.SourceURL = "rsl", rsl.RecordURL(id)
			lives = s.rememberedLives(id)
		}
	}
	if len(f.Errors) > 0 {
		s.renderEditionForm(w, f)
		return
	}
	id, err := s.st.AddEdition(&f.E)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.saveLives(lives)
	// Издание уже записано: сбой обложки или экземпляра — не 500, иначе повторная
	// отправка формы заведёт дубль. Карточка откроется с предупреждением.
	var warn []string
	if err := s.saveCover(id, &f); err != nil {
		log.Printf("обложка издания %d: %v", id, err)
		warn = append(warn, "cover")
	}
	if f.WithCopy {
		if err := s.saveCopy(id, f.Copy, &catalog.Copy{}); err != nil {
			log.Printf("экземпляр издания %d: %v", id, err)
			warn = append(warn, "copy")
		}
	}
	if len(warn) > 0 {
		http.Redirect(w, r, fmt.Sprintf("/book/%d?warn=%s", id, strings.Join(warn, ",")), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d?added", id), http.StatusSeeOther)
}

func (s *server) editForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	e, err := s.st.Edition(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	f := editionForm{Title: "Правка: " + e.Title, Action: fmt.Sprintf("/book/%d/edit", id), E: e}
	// «Дополнить из РГБ»: пустые поля заполняются записью, заполненные остаются как есть —
	// сохраняет человек, посмотрев, что добавилось
	if ref := strings.TrimSpace(r.URL.Query().Get("rsl")); ref != "" {
		c, err := s.rslCard(r, ref)
		if err != nil {
			f.Errors = append(f.Errors, "РГБ: "+err.Error())
			f.RSL = ref // ссылка остаётся в поле — поправить опечатку, а не вставлять заново
		} else {
			rep := fillEmpty(&f.E, c.Edition)
			f.Fill, f.RSL = &rep, c.Edition.SourceURL
			// откуда дополнено — в заметки формы: видно до сохранения, можно убрать
			if line := "Дополнено из РГБ: " + f.RSL; !strings.Contains(f.E.Notes, f.RSL) {
				if f.E.Notes != "" {
					line = "\n" + line
				}
				f.E.Notes += line
			}
		}
	}
	f.People, f.ISBNs, f.Genres = peopleByRole(f.E.People), isbnList(f.E.ISBNs), strings.Join(f.E.Genres, ", ")
	if f.E.Year != 0 {
		f.Year = strconv.Itoa(f.E.Year)
	}
	s.renderEditionForm(w, f)
}

func (s *server) edit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	f := editionForm{Title: "Правка", Action: fmt.Sprintf("/book/%d/edit", id)}
	if err := parseForm(w, r); err != nil {
		f.Errors = []string{err.Error()}
		if f.E, err = s.st.Edition(id); err != nil {
			s.fail(w, err)
			return
		}
		f.People, f.ISBNs, f.Genres = peopleByRole(f.E.People), isbnList(f.E.ISBNs), strings.Join(f.E.Genres, ", ")
		s.renderEditionForm(w, f)
		return
	}
	readEdition(r, &f)
	readCover(r, &f)
	f.E.ID = id
	// дополнено из РГБ — годы жизни людей из той же записи; ссылка на запись уже стоит в
	// заметках формы, и человек мог её убрать — заметки берутся как есть
	f.RSL = r.FormValue("rsl")
	lives := onlyPeople(s.rememberedLives(f.RSL), f.E.People)
	if len(f.Errors) > 0 {
		// форма возвращается с ошибкой — текущую обложку показываем, а не теряем
		if e, err := s.st.Edition(id); err == nil {
			f.E.Cover = e.Cover
		}
		s.renderEditionForm(w, f)
		return
	}
	if err := s.st.UpdateEdition(&f.E); err != nil {
		s.fail(w, err)
		return
	}
	s.saveLives(lives)
	if err := s.saveCover(id, &f); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", id), http.StatusSeeOther)
}

var reFirstNumber = regexp.MustCompile(`\d+(?:[ \x{00A0}\x{202F}]\d{3})*`)

// formInt читает целое поле формы: пусто — 0, «543 с.», «100 000» — понимаются,
// мусор — ошибка формы с названием поля.
func formInt(r *http.Request, name, title string, errs *[]string) int {
	// первое число поля, пробелы между разрядами — внутри числа: «543 с. + 16 л. ил.» → 543,
	// «100 000 экз.» → 100000; склеить все цифры значило бы получить 54316 страниц
	raw := reFirstNumber.FindString(r.FormValue(name))
	raw = strings.Map(func(c rune) rune {
		if c >= '0' && c <= '9' {
			return c
		}
		return -1
	}, raw)
	if raw == "" {
		if strings.TrimSpace(r.FormValue(name)) != "" {
			*errs = append(*errs, title+" — число")
		}
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		*errs = append(*errs, title+" — слишком большое число")
		return 0
	}
	return n
}

func isbnList(ns []isbn.ISBN) string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Norm
	}
	return strings.Join(out, ", ")
}

// --- экземпляры ---

func (s *server) addCopy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.st.Edition(id); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.saveCopy(id, readCopy(r), &catalog.Copy{}); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d?added", id), http.StatusSeeOther)
}

type copyPage struct {
	Title   string
	E       catalog.Edition
	C       catalog.ShelvedCopy
	Fields  copyFields
	Shelves []catalog.ShelfRow
}

func (s *server) copyForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.st.CopyByID(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	e, err := s.st.Edition(c.EditionID)
	if err != nil {
		s.fail(w, err)
		return
	}
	shs, err := s.st.Shelves()
	if err != nil {
		s.fail(w, err)
		return
	}
	f := copyFields{Room: c.Room, Bookcase: c.Bookcase, Shelf: c.Shelf, Condition: c.Condition, Notes: c.Notes,
		NoLend: c.NoLend, Cherished: c.Cherished, Read: c.Read, Grade: c.Grade}
	s.render(w, "copy_form", copyPage{"Экземпляр: " + e.Title, e, c, f, shs})
}

func (s *server) editCopy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.st.CopyByID(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	cp := c.Copy
	cp.Binding = strings.TrimSpace(r.FormValue("binding"))
	cp.Bookplate = strings.TrimSpace(r.FormValue("bookplate"))
	cp.Inscriptions = strings.TrimSpace(r.FormValue("inscriptions"))
	cp.History = strings.TrimSpace(r.FormValue("history"))
	if err := s.saveCopy(c.EditionID, readCopy(r), &cp); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", c.EditionID), http.StatusSeeOther)
}

func (s *server) deleteCopy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.st.CopyByID(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st.DeleteCopy(id); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", c.EditionID), http.StatusSeeOther)
}

// copyFieldsData — данные общего блока полей экземпляра: значения и подсказки полок
// без повторов (в комнате много шкафов, а подсказка «Кабинет» нужна одна).
type copyFieldsData struct {
	C                            copyFields
	Rooms, Bookcases, ShelfNames []string
}

func newCopyFieldsData(shs []catalog.ShelfRow, c copyFields) copyFieldsData {
	d := copyFieldsData{C: c}
	seen := map[string]bool{}
	add := func(list *[]string, kind, v string) {
		if v != "" && !seen[kind+v] {
			seen[kind+v] = true
			*list = append(*list, v)
		}
	}
	for _, sh := range shs {
		add(&d.Rooms, "r", sh.Room)
		add(&d.Bookcases, "b", sh.Bookcase)
		add(&d.ShelfNames, "s", sh.Shelf)
	}
	return d
}

// --- сканер ---

func (s *server) scan(w http.ResponseWriter, r *http.Request) {
	s.render(w, "scan", struct{ Title string }{"Сканер ISBN"})
}
