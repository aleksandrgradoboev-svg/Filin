package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// ruDate показывает ГГГГ-ММ-ДД как ДД.ММ.ГГГГ.
func ruDate(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format("02.01.2006")
}

func (s *server) lend(w http.ResponseWriter, r *http.Request) {
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
	l := catalog.Loan{
		CopyID:   id,
		Borrower: r.FormValue("borrower"),
		LentOn:   r.FormValue("lent_on"),
		DueOn:    r.FormValue("due_on"),
		Note:     strings.TrimSpace(r.FormValue("note")),
	}
	if _, err := s.st.Lend(&l); err != nil {
		// ошибка ввода — карточка с открытой формой выдачи и текстом ошибки
		if catalog.IsInput(err) {
			s.renderBook(w, bookPage{E: catalog.Edition{ID: c.EditionID}, LendErr: err.Error(), LendCopy: id})
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", c.EditionID), http.StatusSeeOther)
}

func (s *server) returnLoan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	copyID, err := s.st.Return(id, r.FormValue("returned_on"))
	if err != nil {
		s.fail(w, err)
		return
	}
	// вернуться туда, откуда нажали: в карточку или в список «На руках»
	back := r.FormValue("back")
	// только свой адрес: «//сайт» и «/\сайт» браузер понимает как чужой сайт
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.HasPrefix(back, "/\\") {
		c, err := s.st.CopyByID(copyID)
		if err != nil {
			s.fail(w, err)
			return
		}
		back = fmt.Sprintf("/book/%d", c.EditionID)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

type loansPage struct {
	Title  string
	Today  string
	Loans  []catalog.LoanRow
	People []personLoans // кто сколько держит — для перехода к человеку
}

type personLoans struct {
	Name  string
	Count int
}

func (s *server) loans(w http.ResponseWriter, r *http.Request) {
	ls, err := s.st.OnLoan()
	if err != nil {
		s.fail(w, err)
		return
	}
	p := loansPage{Title: "На руках", Today: catalog.Today(), Loans: ls}
	idx := map[string]int{}
	for _, l := range ls {
		if i, ok := idx[l.Borrower]; ok {
			p.People[i].Count++
			continue
		}
		idx[l.Borrower] = len(p.People)
		p.People = append(p.People, personLoans{l.Borrower, 1})
	}
	s.render(w, "loans", p)
}

// loanListData — данные общего списка выдач: куда вернуться после «Вернули».
type loanListData struct {
	Loans []catalog.LoanRow
	Today string
	Back  string
}

func newLoanListData(ls []catalog.LoanRow, today, back string) loanListData {
	return loanListData{ls, today, back}
}

type borrowerPage struct {
	Title    string
	Name     string
	Back     string // адрес этой же страницы
	Today    string
	OnHand   []catalog.LoanRow
	Returned []catalog.LoanRow
}

func (s *server) borrower(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Redirect(w, r, "/loans", http.StatusSeeOther)
		return
	}
	ls, err := s.st.LoansOf(name)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := borrowerPage{Title: name, Name: name, Back: whoURL(name), Today: catalog.Today()}
	for _, l := range ls {
		if l.ReturnedOn == "" {
			p.OnHand = append(p.OnHand, l)
		} else {
			p.Returned = append(p.Returned, l)
		}
	}
	s.render(w, "borrower", p)
}

// whoURL — адрес страницы человека.
func whoURL(name string) string { return "/loans/who?name=" + url.QueryEscape(name) }

// daysSince — сколько дней книга на руках, для подписи «уже 40 дней».
func daysSince(d, today string) int {
	a, err1 := time.Parse(time.DateOnly, d)
	b, err2 := time.Parse(time.DateOnly, today)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(b.Sub(a).Hours() / 24)
}
