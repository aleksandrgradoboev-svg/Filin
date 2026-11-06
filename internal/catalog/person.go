package catalog

import (
	"database/sql"
	"errors"
	"strings"
)

// PersonInfo — справка о человеке.
type PersonInfo struct {
	Name  string
	Life  string // «1876–1916»
	About string
	Link  string
}

// PersonBook — издание человека и его роль в нём.
type PersonBook struct {
	Role string
	Row
}

// Person — справка и все издания человека: сначала, где он автор, внутри — по году.
func (s *Store) Person(name string) (PersonInfo, []PersonBook, error) {
	name = strings.Join(strings.Fields(name), " ")
	p := PersonInfo{Name: name}
	err := s.db.QueryRow(`SELECT life, about, link FROM person WHERE name = ?`, name).Scan(&p.Life, &p.About, &p.Link)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, nil, err
	}
	rows, err := s.db.Query(`SELECT ep.role, e.id, e.title, e.publisher, COALESCE(e.year, 0), e.volume, e.cover_path,
			COALESCE((SELECT group_concat(p.name, ', ' ORDER BY p.position) FROM edition_person p
			          WHERE p.edition_id = e.id AND p.role = 'author'), ''),
			(SELECT COUNT(*) FROM book_copy c WHERE c.edition_id = e.id)
		FROM edition_person ep JOIN edition e ON e.id = ep.edition_id
		WHERE ep.name = ?
		ORDER BY ep.role <> 'author', ep.role, e.year IS NULL, e.year, fold(e.title)`, name)
	if err != nil {
		return p, nil, err
	}
	defer rows.Close()
	var out []PersonBook
	for rows.Next() {
		var b PersonBook
		if err := rows.Scan(&b.Role, &b.ID, &b.Title, &b.Publisher, &b.Year, &b.Volume, &b.Cover, &b.Authors,
			&b.Copies); err != nil {
			return p, nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return p, nil, err
	}
	if len(out) == 0 && p.Life == "" && p.About == "" && p.Link == "" {
		return p, nil, ErrNotFound
	}
	return p, out, nil
}

// SavePerson записывает справку; пустая справка удаляется.
func (s *Store) SavePerson(p PersonInfo) error {
	p.Name = strings.Join(strings.Fields(p.Name), " ")
	if p.Name == "" {
		return InputError("нет имени")
	}
	p.Life, p.About, p.Link = strings.TrimSpace(p.Life), strings.TrimSpace(p.About), strings.TrimSpace(p.Link)
	if p.Link != "" && !strings.HasPrefix(p.Link, "http://") && !strings.HasPrefix(p.Link, "https://") {
		return InputError("ссылка должна начинаться с http:// или https://")
	}
	if p.Life == "" && p.About == "" && p.Link == "" {
		_, err := s.db.Exec(`DELETE FROM person WHERE name = ?`, p.Name)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO person (name, life, about, link) VALUES (?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET life = excluded.life, about = excluded.about, link = excluded.link`,
		p.Name, p.Life, p.About, p.Link)
	return err
}

// PersonCount — человек и сколько у него изданий.
type PersonCount struct {
	Name  string
	Life  string
	Count int
}

// People — все люди из карточек: авторы (role author) отдельно от остальных.
func (s *Store) People(authorsOnly bool) ([]PersonCount, error) {
	where := ""
	if authorsOnly {
		where = `WHERE ep.role = 'author'`
	}
	rows, err := s.db.Query(`SELECT ep.name, COALESCE(p.life, ''), COUNT(DISTINCT ep.edition_id)
		FROM edition_person ep LEFT JOIN person p ON p.name = ep.name `+where+`
		GROUP BY ep.name ORDER BY fold(ep.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonCount
	for rows.Next() {
		var c PersonCount
		if err := rows.Scan(&c.Name, &c.Life, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
