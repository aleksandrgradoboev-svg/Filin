package catalog

import (
	"database/sql"
	"errors"
	"strings"
)

// Grades — шкала состояния экземпляра: индекс — оценка, 0 — не оценено.
var Grades = []string{"", "плохое", "удовлетворительное", "хорошее", "очень хорошее", "отличное"}

// GradeName — название оценки состояния; пусто — не оценено или вне шкалы.
func GradeName(g int) string {
	if g < 1 || g >= len(Grades) {
		return ""
	}
	return Grades[g]
}

// Valuation — оценка экземпляра в рублях на дату. Оценки копятся, действующая —
// последняя по дате: цены меняются, и прежняя оценка — тоже сведения.
type Valuation struct {
	ID        int64
	CopyID    int64
	Amount    int // рубли
	ValuedOn  string
	Basis     string
	SourceURL string
}

// currentValuation — id действующей оценки экземпляра c (подзапрос для JOIN).
const currentValuation = `SELECT v2.id FROM valuation v2 WHERE v2.copy_id = c.id
	ORDER BY v2.valued_on DESC, v2.id DESC LIMIT 1`

// AddValuation записывает оценку. Пустая дата — сегодня.
func (s *Store) AddValuation(v *Valuation) (int64, error) {
	if v.Amount < 0 {
		return 0, InputError("оценка не может быть отрицательной")
	}
	if v.ValuedOn == "" {
		v.ValuedOn = Today()
	}
	if err := checkDate(v.ValuedOn); err != nil {
		return 0, err
	}
	if v.ValuedOn > Today() {
		return 0, InputError("дата оценки ещё не наступила")
	}
	v.Basis, v.SourceURL = strings.TrimSpace(v.Basis), strings.TrimSpace(v.SourceURL)
	if v.SourceURL != "" && !strings.HasPrefix(v.SourceURL, "http://") && !strings.HasPrefix(v.SourceURL, "https://") {
		return 0, InputError("ссылка должна начинаться с http:// или https://")
	}
	res, err := s.db.Exec(`INSERT INTO valuation (copy_id, amount, valued_on, basis, source_url) VALUES (?, ?, ?, ?, ?)`,
		v.CopyID, v.Amount, v.ValuedOn, v.Basis, v.SourceURL)
	if err != nil {
		return 0, err
	}
	v.ID, err = res.LastInsertId()
	return v.ID, err
}

// DeleteValuation убирает ошибочную оценку и возвращает экземпляр, к которому она относилась.
func (s *Store) DeleteValuation(id int64) (copyID int64, err error) {
	err = s.db.QueryRow(`DELETE FROM valuation WHERE id = ? RETURNING copy_id`, id).Scan(&copyID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return copyID, err
}

// CopyValuations — все оценки экземпляра, новые сверху.
func (s *Store) CopyValuations(copyID int64) ([]Valuation, error) {
	rows, err := s.db.Query(`SELECT id, copy_id, amount, valued_on, basis, source_url FROM valuation
		WHERE copy_id = ? ORDER BY valued_on DESC, id DESC`, copyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Valuation
	for rows.Next() {
		var v Valuation
		if err := rows.Scan(&v.ID, &v.CopyID, &v.Amount, &v.ValuedOn, &v.Basis, &v.SourceURL); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ValueSummary — итог по библиотеке.
type ValueSummary struct {
	Total     int // сумма действующих оценок, рубли
	Valued    int // экземпляров с оценкой
	Copies    int // всего экземпляров
	Cherished int // «дорожу»
}

func (s *Store) ValueSummary() (ValueSummary, error) {
	var v ValueSummary
	err := s.db.QueryRow(`SELECT COALESCE(SUM(v.amount), 0), COUNT(v.id), COUNT(*), COALESCE(SUM(c.cherished), 0)
		FROM book_copy c LEFT JOIN valuation v ON v.id = (`+currentValuation+`)`).
		Scan(&v.Total, &v.Valued, &v.Copies, &v.Cherished)
	return v, err
}

// ValueRow — экземпляр в списке ценного.
type ValueRow struct {
	CopyID    int64
	EditionID int64
	Title     string
	Volume    string
	Authors   string
	Cover     string
	Grade     int
	Cherished bool
	Amount    int    // 0 при Valued == false
	ValuedOn  string // пусто — не оценён
}

// MostValuable — экземпляры по убыванию оценки; cherishedOnly — только «дорожу»
// (там и неоценённые: памятное не обязано иметь цену).
func (s *Store) MostValuable(limit int, cherishedOnly bool) ([]ValueRow, error) {
	where := `v.id IS NOT NULL`
	if cherishedOnly {
		where = `c.cherished = 1`
	}
	rows, err := s.db.Query(`SELECT c.id, e.id, e.title, e.volume,
			COALESCE((SELECT group_concat(p.name, ', ' ORDER BY p.position) FROM edition_person p
			          WHERE p.edition_id = e.id AND p.role = 'author'), ''),
			e.cover_path, COALESCE(c.grade, 0), c.cherished, COALESCE(v.amount, 0), COALESCE(v.valued_on, '')
		FROM book_copy c JOIN edition e ON e.id = c.edition_id
		LEFT JOIN valuation v ON v.id = (`+currentValuation+`)
		WHERE `+where+`
		ORDER BY COALESCE(v.amount, -1) DESC, fold(e.title) LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ValueRow
	for rows.Next() {
		var r ValueRow
		if err := rows.Scan(&r.CopyID, &r.EditionID, &r.Title, &r.Volume, &r.Authors, &r.Cover, &r.Grade,
			&r.Cherished, &r.Amount, &r.ValuedOn); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
