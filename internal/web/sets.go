package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Собрания сочинений: список с комплектностью, заведение, страница собрания с местами.
// Книга ставится на место из своей карточки (bookPage.Places), снимается — там же или здесь.

type setsPage struct {
	Title string
	Sets  []catalog.SetRow
	Err   string
	Form  setForm // что человек ввёл, если заведение не удалось
}

type setForm struct {
	Title, Publisher, YearFrom, YearTo, Volumes, Notes string
}

// percent — доля для полосы комплектности, целыми процентами; пустое собрание — 0.
func percent(have, of int) int {
	if of <= 0 {
		return 0
	}
	return have * 100 / of
}

func (s *server) sets(w http.ResponseWriter, r *http.Request) {
	s.renderSets(w, setsPage{})
}

func (s *server) renderSets(w http.ResponseWriter, p setsPage) {
	var err error
	if p.Sets, err = s.st.Sets(); err != nil {
		s.fail(w, err)
		return
	}
	p.Title = "Собрания"
	s.render(w, "sets", p)
}

// yearOK — год издания правдоподобен; одно правило для формы книги, приложения и собраний.
func yearOK(y int) bool { return y >= 1400 && y <= 2100 }

// optYear — необязательный год из формы: пусто — 0.
func optYear(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, true
	}
	y, err := strconv.Atoi(v)
	return y, err == nil && yearOK(y)
}

func (s *server) createSet(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		s.fail(w, catalog.InputError(err.Error()))
		return
	}
	f := setForm{Title: r.FormValue("title"), Publisher: r.FormValue("publisher"), YearFrom: r.FormValue("year_from"),
		YearTo: r.FormValue("year_to"), Volumes: r.FormValue("volumes"), Notes: r.FormValue("notes")}
	retry := func(msg string) { s.renderSets(w, setsPage{Err: msg, Form: f}) }
	from, ok1 := optYear(f.YearFrom)
	to, ok2 := optYear(f.YearTo)
	if !ok1 || !ok2 {
		retry("год — четыре цифры, например 1957")
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(f.Volumes))
	if err != nil {
		retry("сколько томов — числом, например 12")
		return
	}
	c := catalog.Collection{Title: f.Title, Publisher: f.Publisher, YearFrom: from, YearTo: to, VolumesAnnounced: n, Notes: f.Notes}
	id, err := s.st.CreateSet(&c)
	if err != nil {
		if catalog.IsInput(err) {
			retry(err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/sets/%d", id), http.StatusSeeOther)
}

type setPage struct {
	Title   string
	C       catalog.Collection
	Places  []catalog.Place
	Have    int
	Missing []string // «т. 3», «т. 9, кн. 2»
	Err     string
}

func (s *server) set(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderSet(w, id, r.URL.Query().Get("err"))
}

func (s *server) renderSet(w http.ResponseWriter, id int64, msg string) {
	c, places, err := s.st.Set(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := setPage{Title: c.Title, C: c, Places: places, Err: msg}
	for _, pl := range places {
		if pl.Have {
			p.Have++
		} else {
			p.Missing = append(p.Missing, pl.Name())
		}
	}
	s.render(w, "set", p)
}

// setAction — правка собрания формой: при ошибке ввода страница собрания с сообщением.
func (s *server) setAction(do func(r *http.Request, id int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			s.fail(w, err)
			return
		}
		if err := parseForm(w, r); err != nil {
			s.fail(w, catalog.InputError(err.Error()))
			return
		}
		if err := do(r, id); err != nil {
			if catalog.IsInput(err) {
				s.renderSet(w, id, err.Error())
				return
			}
			s.fail(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/sets/%d", id), http.StatusSeeOther)
	}
}

func (s *server) splitVolume(r *http.Request, id int64) error {
	v, err1 := strconv.Atoi(strings.TrimSpace(r.FormValue("volume")))
	b, err2 := strconv.Atoi(strings.TrimSpace(r.FormValue("books")))
	if err1 != nil || err2 != nil {
		return catalog.InputError("том и число книг — числами")
	}
	return s.st.SplitVolume(id, v, b)
}

func (s *server) addPlace(r *http.Request, id int64) error {
	return s.st.AddPlace(id, r.FormValue("label"))
}

func (s *server) deleteSet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.st.DeleteSet(id); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/sets", http.StatusSeeOther)
}

// removePlace убирает место собрания id; место выбрано в списке на странице собрания.
func (s *server) removePlace(r *http.Request, id int64) error {
	place, err := strconv.ParseInt(r.FormValue("place"), 10, 64)
	if err != nil {
		return catalog.InputError("выберите место")
	}
	_, places, err := s.st.Set(id)
	if err != nil {
		return err
	}
	for _, p := range places {
		if p.ID == place {
			_, err := s.st.DeletePlace(place)
			return err
		}
	}
	return catalog.InputError("такого места в этом собрании нет")
}

// unplace снимает издание с места; возвращает туда, откуда пришли: в собрание или в карточку.
func (s *server) unplace(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := parseForm(w, r); err != nil {
		s.fail(w, catalog.InputError(err.Error()))
		return
	}
	ed, err := strconv.ParseInt(r.FormValue("edition"), 10, 64)
	if err != nil {
		s.fail(w, catalog.InputError("не указано издание"))
		return
	}
	setID, err := s.st.Unplace(id, ed)
	if err != nil {
		s.fail(w, err)
		return
	}
	back := fmt.Sprintf("/sets/%d", setID)
	if r.FormValue("back") == "book" {
		back = fmt.Sprintf("/book/%d", ed)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// putInSet — «В собрание» из карточки книги: собрание, том(а) и, если том разделён, книга.
func (s *server) putInSet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := parseForm(w, r); err != nil {
		s.fail(w, catalog.InputError(err.Error()))
		return
	}
	retry := func(msg string) {
		s.renderBook(w, bookPage{E: catalog.Edition{ID: id}, SetErr: msg})
	}
	setID, err := strconv.ParseInt(r.FormValue("set"), 10, 64)
	if err != nil {
		retry("выберите собрание")
		return
	}
	book := 0
	if b := strings.TrimSpace(r.FormValue("book")); b != "" {
		if book, err = strconv.Atoi(b); err != nil || book < 1 {
			retry("книга в томе — номер, например 2, или пусто")
			return
		}
	}
	refs, err := catalog.ParseVolumes(r.FormValue("volumes"), book)
	if err == nil {
		err = s.st.PutInSet(id, setID, refs)
	}
	if err != nil {
		if catalog.IsInput(err) {
			retry(err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", id), http.StatusSeeOther)
}
