package catalog

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Loan — выдача экземпляра почитать.
type Loan struct {
	ID         int64
	CopyID     int64
	Borrower   string
	LentOn     string // ГГГГ-ММ-ДД
	DueOn      string // пусто — без срока
	ReturnedOn string // пусто — книга на руках
	Note       string
}

// Overdue — срок прошёл, а книга не вернулась. today — ГГГГ-ММ-ДД.
func (l Loan) Overdue(today string) bool {
	return l.ReturnedOn == "" && l.DueOn != "" && l.DueOn < today
}

// Today — сегодняшняя дата в виде, в котором её хранит выдача.
func Today() string { return time.Now().Format(time.DateOnly) }

// InputError — ошибка в том, что ввёл человек: её показывают в форме, а не как сбой.
type InputError string

func (e InputError) Error() string { return string(e) }

// IsInput — ошибка ввода, а не сбой базы.
func IsInput(err error) bool {
	var ie InputError
	return errors.As(err, &ie)
}

// ErrLent — экземпляр уже на руках.
var ErrLent error = InputError("экземпляр уже на руках — сначала отметьте возврат")

// Lend отдаёт экземпляр почитать.
func (s *Store) Lend(l *Loan) (int64, error) {
	l.Borrower = strings.TrimSpace(l.Borrower)
	if l.Borrower == "" {
		return 0, InputError("кому — нужно имя")
	}
	if l.LentOn == "" {
		l.LentOn = Today()
	}
	if err := checkDate(l.LentOn); err != nil {
		return 0, err
	}
	if l.DueOn != "" {
		if err := checkDate(l.DueOn); err != nil {
			return 0, err
		}
		if l.DueOn < l.LentOn {
			return 0, InputError("срок раньше дня выдачи")
		}
	}
	var lent bool
	if err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM loan WHERE copy_id = ? AND returned_on IS NULL)`,
		l.CopyID).Scan(&lent); err != nil {
		return 0, err
	}
	if lent {
		return 0, ErrLent
	}
	res, err := s.db.Exec(`INSERT INTO loan (copy_id, borrower, lent_on, due_on, note) VALUES (?, ?, ?, ?, ?)`,
		l.CopyID, l.Borrower, l.LentOn, nullStr(l.DueOn), l.Note)
	if err != nil {
		// гонка двух выдач одного экземпляра — её ловит уникальный индекс loan_active
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrLent
		}
		return 0, err
	}
	l.ID, err = res.LastInsertId()
	return l.ID, err
}

// Return отмечает возврат. Пустая дата — сегодня. Возвращает экземпляр выдачи.
func (s *Store) Return(loanID int64, on string) (copyID int64, err error) {
	if on == "" {
		on = Today()
	}
	if err := checkDate(on); err != nil {
		return 0, err
	}
	if on > Today() {
		return 0, InputError("дата возврата ещё не наступила")
	}
	var lentOn string
	err = s.db.QueryRow(`SELECT copy_id, lent_on FROM loan WHERE id = ? AND returned_on IS NULL`, loanID).
		Scan(&copyID, &lentOn)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if on < lentOn {
		return 0, InputError("вернули раньше, чем отдали")
	}
	_, err = s.db.Exec(`UPDATE loan SET returned_on = ? WHERE id = ?`, on, loanID)
	return copyID, err
}

// LoanRow — выдача с названием книги, для списков.
type LoanRow struct {
	Loan
	EditionID int64
	Title     string
	Volume    string
	Cover     string
}

const loanRowSelect = `SELECT l.id, l.copy_id, l.borrower, l.lent_on, COALESCE(l.due_on, ''),
		COALESCE(l.returned_on, ''), l.note, e.id, e.title, e.volume, e.cover_path
	FROM loan l JOIN book_copy c ON c.id = l.copy_id JOIN edition e ON e.id = c.edition_id `

func (s *Store) loanRows(q string, args ...any) ([]LoanRow, error) {
	rows, err := s.db.Query(loanRowSelect+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoanRow
	for rows.Next() {
		var r LoanRow
		if err := rows.Scan(&r.ID, &r.CopyID, &r.Borrower, &r.LentOn, &r.DueOn, &r.ReturnedOn, &r.Note,
			&r.EditionID, &r.Title, &r.Volume, &r.Cover); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OnLoan — всё, что сейчас на руках: сначала просроченные, потом давно отданные.
func (s *Store) OnLoan() ([]LoanRow, error) {
	return s.loanRows(`WHERE l.returned_on IS NULL
		ORDER BY (l.due_on IS NOT NULL AND l.due_on < ?) DESC, l.lent_on, l.id`, Today())
}

// LoansOf — все выдачи человеку: сначала те, что на руках.
func (s *Store) LoansOf(borrower string) ([]LoanRow, error) {
	return s.loanRows(`WHERE l.borrower = ? ORDER BY l.returned_on IS NOT NULL, l.lent_on DESC, l.id DESC`,
		strings.TrimSpace(borrower))
}

// CopyLoans — история выдач экземпляра, новые сверху.
func (s *Store) CopyLoans(copyID int64) ([]Loan, error) {
	rows, err := s.loanRows(`WHERE l.copy_id = ? ORDER BY l.lent_on DESC, l.id DESC`, copyID)
	if err != nil {
		return nil, err
	}
	out := make([]Loan, len(rows))
	for i, r := range rows {
		out[i] = r.Loan
	}
	return out, nil
}

// Borrowers — кому уже давали, для подсказок в форме.
func (s *Store) Borrowers() ([]string, error) {
	rows, err := s.db.Query(`SELECT borrower FROM loan GROUP BY borrower ORDER BY MAX(lent_on) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func checkDate(d string) error {
	if _, err := time.Parse(time.DateOnly, d); err != nil {
		return InputError("дата — в виде ГГГГ-ММ-ДД")
	}
	return nil
}
