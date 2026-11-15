// Package export — выгрузка каталога, чтобы библиотеку можно было забрать из Филина:
// таблицей CSV (открыть в Excel) и целиком в JSON (перенести в другую программу).
//
// Резервная копия (пакет backup) — другое: она для восстановления самого Филина и везёт
// базу как есть. Выгрузка — для человека и чужих программ, поэтому поля названы словами,
// а не колонками базы. Фото обложек не выгружаются (они в резервной копии), привязки
// телефонов — тем более.
package export

import (
	"errors"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Format и Version — метка файла JSON: по ней чужая программа узнаёт, что читает.
// Version растёт, когда поля меняются несовместимо (переименованы или убраны).
const (
	Format  = "filin-catalog"
	Version = 1
)

// Library — весь каталог для JSON.
type Library struct {
	Format     string   `json:"format"`
	Version    int      `json:"version"`
	ExportedAt string   `json:"exported_at"` // RFC 3339, UTC
	Program    string   `json:"program"`     // версия Филина, снявшего выгрузку
	Books      []Book   `json:"books"`
	People     []Person `json:"people"`
	Sets       []Set    `json:"sets"`
}

// Book — издание и его экземпляры.
type Book struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	Volume        string     `json:"volume"`
	People        []Role     `json:"people"`
	Publisher     string     `json:"publisher"`
	City          string     `json:"city"`
	Year          int        `json:"year,omitempty"`
	ISBNs         []ISBN     `json:"isbns"`
	Series        *Series    `json:"series"`
	Sets          []SetPlace `json:"sets"`
	Genres        []string   `json:"genres"`
	Subjects      []string   `json:"subjects"` // уровни через « -- », как в РГБ
	BBK           string     `json:"bbk"`
	UDK           string     `json:"udk"`
	AuthorSign    string     `json:"author_sign"`
	CoverType     string     `json:"cover_type"`
	Format        string     `json:"format"`
	Pages         int        `json:"pages,omitempty"`
	OrigTitle     string     `json:"orig_title"`
	OrigLanguage  string     `json:"orig_language"`
	Language      string     `json:"language"`
	EditionNote   string     `json:"edition_note"`
	Circulation   int        `json:"circulation,omitempty"`
	PrintPrice    string     `json:"print_price"`
	Printer       string     `json:"printer"`
	AgeRating     string     `json:"age_rating"`
	Annotation    string     `json:"annotation"`
	Contents      string     `json:"contents"`
	Paper         string     `json:"paper"`
	Design        string     `json:"design"`
	Illustrations string     `json:"illustrations"`
	Dimensions    string     `json:"dimensions"`
	Weight        int        `json:"weight,omitempty"`
	Source        string     `json:"source"`
	SourceURL     string     `json:"source_url"`
	LiveLibID     string     `json:"livelib_id"`
	Notes         string     `json:"notes"`
	Cover         string     `json:"cover"` // имя файла в папке covers резервной копии; само фото не выгружается
	Copies        []Copy     `json:"copies"`
}

// Role — человек издания: author, editor, translator, illustrator, compiler.
type Role struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type ISBN struct {
	ISBN  string `json:"isbn"`
	Valid bool   `json:"valid"` // false — контрольная цифра не сходится, номер записан как напечатан
}

type Series struct {
	Title  string `json:"title"`
	Number string `json:"number"`
}

// SetPlace — где издание стоит в собрании сочинений: «т. 3, кн. 2».
type SetPlace struct {
	Set   string `json:"set"`
	Place string `json:"place"`
}

// Copy — экземпляр на полке.
type Copy struct {
	ID           int64       `json:"id"`
	Shelf        *Shelf      `json:"shelf"`
	Grade        int         `json:"grade,omitempty"` // 1–6 по шкале alib.ru; 0 — не оценено
	GradeName    string      `json:"grade_name"`
	Condition    string      `json:"condition"`
	Binding      string      `json:"binding"`
	Bookplate    string      `json:"bookplate"`
	Inscriptions string      `json:"inscriptions"`
	History      string      `json:"history"`
	Read         bool        `json:"read"`
	Cherished    bool        `json:"cherished"`
	NoLend       bool        `json:"no_lend"`
	Notes        string      `json:"notes"`
	Loans        []Loan      `json:"loans"`      // новые сверху; у текущей выдачи нет returned_on
	Valuations   []Valuation `json:"valuations"` // новые сверху; действует первая
}

type Shelf struct {
	Room     string `json:"room"`
	Bookcase string `json:"bookcase"`
	Shelf    string `json:"shelf"`
}

type Loan struct {
	Borrower   string `json:"borrower"`
	LentOn     string `json:"lent_on"`
	DueOn      string `json:"due_on,omitempty"`
	ReturnedOn string `json:"returned_on,omitempty"`
	Note       string `json:"note"`
}

// Valuation — оценка в рублях; amount_to есть у диапазона «от – до».
type Valuation struct {
	Amount    int    `json:"amount"`
	AmountTo  int    `json:"amount_to,omitempty"`
	ValuedOn  string `json:"valued_on"`
	Basis     string `json:"basis"`
	SourceURL string `json:"source_url"`
}

// Person — справка о человеке со страницы людей.
type Person struct {
	Name  string `json:"name"`
	Life  string `json:"life"`
	About string `json:"about"`
	Link  string `json:"link"`
}

// Set — собрание сочинений с местами.
type Set struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	Publisher        string  `json:"publisher"`
	YearFrom         int     `json:"year_from,omitempty"`
	YearTo           int     `json:"year_to,omitempty"`
	VolumesAnnounced int     `json:"volumes_announced,omitempty"`
	Notes            string  `json:"notes"`
	Places           []Place `json:"places"`
}

// Place — место собрания и издания (id из books), которые его закрывают; пусто — не хватает.
type Place struct {
	Place string  `json:"place"`
	Books []int64 `json:"books"`
}

// Build собирает весь каталог. Читает по изданию за раз — это тысячи коротких запросов
// к локальной базе, на домашней библиотеке доли секунды.
func Build(st *catalog.Store, program string, now time.Time) (*Library, error) {
	lib := &Library{Format: Format, Version: Version, ExportedAt: now.UTC().Format(time.RFC3339), Program: program,
		Books: []Book{}, People: []Person{}, Sets: []Set{}}
	ids, err := st.EditionIDs()
	if err != nil {
		return nil, err
	}
	// Филин при этом работает: книгу или собрание могут удалить, пока идёт сборка. Удалённое
	// просто не попадает в выгрузку — сбой из-за него оставил бы человека без файла.
	for _, id := range ids {
		b, err := book(st, id)
		if errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		lib.Books = append(lib.Books, b)
	}
	ps, err := st.PeopleInfo()
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		lib.People = append(lib.People, Person(p))
	}
	sets, err := st.Sets()
	if err != nil {
		return nil, err
	}
	for _, r := range sets {
		c, places, err := st.Set(r.ID)
		if errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		set := Set{ID: c.ID, Title: c.Title, Publisher: c.Publisher, YearFrom: c.YearFrom, YearTo: c.YearTo,
			VolumesAnnounced: c.VolumesAnnounced, Notes: c.Notes, Places: []Place{}}
		for _, p := range places {
			pl := Place{Place: p.Name(), Books: []int64{}}
			for _, e := range p.Editions {
				pl.Books = append(pl.Books, e.ID)
			}
			set.Places = append(set.Places, pl)
		}
		lib.Sets = append(lib.Sets, set)
	}
	return lib, nil
}

func book(st *catalog.Store, id int64) (Book, error) {
	e, err := st.Edition(id)
	if err != nil {
		return Book{}, err
	}
	d := e.Details
	b := Book{ID: e.ID, Title: e.Title, Volume: e.Volume, Publisher: e.Publisher, City: e.City, Year: e.Year,
		People: []Role{}, ISBNs: []ISBN{}, Sets: []SetPlace{}, Genres: nonNil(e.Genres), Subjects: nonNil(e.Subjects),
		BBK: e.BBK, UDK: e.UDK, AuthorSign: e.AuthorSign, CoverType: e.CoverType, Format: e.Format, Pages: e.Pages,
		OrigTitle: d.OrigTitle, OrigLanguage: d.OrigLanguage, Language: d.Language, EditionNote: d.EditionNote,
		Circulation: d.Circulation, PrintPrice: d.PrintPrice, Printer: d.Printer, AgeRating: d.AgeRating,
		Annotation: d.Annotation, Contents: d.Contents, Paper: d.Paper, Design: d.Design,
		Illustrations: d.Illustrations, Dimensions: d.Dimensions, Weight: d.Weight,
		Source: e.Source, SourceURL: e.SourceURL, LiveLibID: e.LiveLibID, Notes: e.Notes, Cover: e.Cover,
		Copies: []Copy{}}
	for _, p := range e.People {
		role := p.Role
		if role == "" {
			role = "author"
		}
		b.People = append(b.People, Role{Name: p.Name, Role: role})
	}
	for _, n := range e.ISBNs {
		b.ISBNs = append(b.ISBNs, ISBN{ISBN: n.Norm, Valid: n.Valid})
	}
	if e.Series != "" {
		b.Series = &Series{Title: e.Series, Number: e.SeriesNo}
	}
	places, err := st.EditionPlaces(id)
	if err != nil {
		return b, err
	}
	for _, p := range places {
		b.Sets = append(b.Sets, SetPlace{Set: p.SetTitle, Place: p.Place})
	}
	copies, err := st.Copies(id)
	if err != nil {
		return b, err
	}
	for _, c := range copies {
		x := Copy{ID: c.ID, Grade: c.Grade, GradeName: catalog.GradeName(c.Grade), Condition: c.Condition,
			Binding: c.Binding, Bookplate: c.Bookplate, Inscriptions: c.Inscriptions, History: c.History,
			Read: c.Read, Cherished: c.Cherished, NoLend: c.NoLend, Notes: c.Notes,
			Loans: []Loan{}, Valuations: []Valuation{}}
		if c.ShelfID != 0 {
			x.Shelf = &Shelf{Room: c.Room, Bookcase: c.Bookcase, Shelf: c.Shelf}
		}
		loans, err := st.CopyLoans(c.ID)
		if err != nil {
			return b, err
		}
		for _, l := range loans {
			x.Loans = append(x.Loans, Loan{Borrower: l.Borrower, LentOn: l.LentOn, DueOn: l.DueOn,
				ReturnedOn: l.ReturnedOn, Note: l.Note})
		}
		vals, err := st.CopyValuations(c.ID)
		if err != nil {
			return b, err
		}
		for _, v := range vals {
			x.Valuations = append(x.Valuations, Valuation{Amount: v.Amount, AmountTo: v.AmountTo,
				ValuedOn: v.ValuedOn, Basis: v.Basis, SourceURL: v.SourceURL})
		}
		b.Copies = append(b.Copies, x)
	}
	return b, nil
}

// nonNil — пустой список вместо null: чужой программе проще перебирать, чем проверять.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
