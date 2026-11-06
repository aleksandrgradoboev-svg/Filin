package catalog

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
)

// Genre — жанр со счётчиком изданий.
type Genre struct {
	ID    int64
	Name  string
	Count int
}

// ParseGenres разбирает «приключения, исторический роман; детская» в список без повторов.
func ParseGenres(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		g = strings.Join(strings.Fields(g), " ")
		if g == "" || seen[foldKey(g)] {
			continue
		}
		seen[foldKey(g)] = true
		out = append(out, g)
	}
	return out
}

func foldKey(s string) string { return strings.ReplaceAll(strings.ToLower(s), "ё", "е") }

// writeGenres переписывает жанры издания внутри транзакции. Жанр, записанный иначе
// по регистру или через «ё», — тот же жанр: берётся уже заведённый.
func writeGenres(tx *sql.Tx, id int64, genres []string) error {
	if _, err := tx.Exec(`DELETE FROM edition_genre WHERE edition_id = ?`, id); err != nil {
		return err
	}
	for i, g := range genres {
		gid, err := genreID(tx, g, false)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO edition_genre (edition_id, genre_id, position) VALUES (?, ?, ?)`,
			id, gid, i+1); err != nil {
			return err
		}
	}
	// свой жанр без единой книги — опечатка, оставшаяся в подсказках; справочный остаётся
	_, err := tx.Exec(`DELETE FROM genre WHERE livelib = 0 AND id NOT IN (SELECT genre_id FROM edition_genre)`)
	return err
}

// genreID находит жанр по названию без учёта регистра и «ё» или заводит его.
// fromLiveLib помечает жанр справочным, в том числе уже заведённый руками.
func genreID(tx *sql.Tx, name string, fromLiveLib bool) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM genre WHERE fold(name) = fold(?)`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`INSERT INTO genre (name, livelib) VALUES (?, ?) RETURNING id`, name, fromLiveLib).Scan(&id)
		return id, err
	}
	if err == nil && fromLiveLib {
		_, err = tx.Exec(`UPDATE genre SET livelib = 1 WHERE id = ?`, id)
	}
	return id, err
}

// GenreSection — раздел справочника с жанрами.
type GenreSection struct {
	Name     string
	Children []string
}

// ImportGenreTree заводит справочник жанров: разделы и жанры в них. Повторный импорт
// ничего не задваивает. Возвращает, сколько жанров стало справочными.
func (s *Store) ImportGenreTree(tree []GenreSection) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seen := map[int64]bool{}
	for _, sec := range tree {
		pid, err := genreID(tx, sec.Name, true)
		if err != nil {
			return 0, err
		}
		seen[pid] = true
		for _, c := range sec.Children {
			cid, err := genreID(tx, c, true)
			if err != nil {
				return 0, err
			}
			seen[cid] = true
			if cid == pid {
				continue
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO genre_group (parent_id, child_id) VALUES (?, ?)`, pid, cid); err != nil {
				return 0, err
			}
		}
	}
	return len(seen), tx.Commit()
}

// GenreTreeNode — раздел справочника для страницы жанров: сколько книг в нём и в его жанрах.
type GenreTreeNode struct {
	Genre
	Children []Genre
	Total    int // книг в разделе и его жанрах (книга с двумя жанрами раздела — дважды)
}

// GenreTree — разделы справочника с жанрами и счётчиками; жанры вне разделов (заведённые
// руками) — отдельным разделом без названия в конце.
func (s *Store) GenreTree() ([]GenreTreeNode, error) {
	all, err := s.Genres()
	if err != nil {
		return nil, err
	}
	byID := map[int64]Genre{}
	for _, g := range all {
		byID[g.ID] = g
	}
	rows, err := s.db.Query(`SELECT parent_id, child_id FROM genre_group`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kids := map[int64][]Genre{}
	isChild := map[int64]bool{}
	for rows.Next() {
		var p, c int64
		if err := rows.Scan(&p, &c); err != nil {
			return nil, err
		}
		kids[p] = append(kids[p], byID[c])
		isChild[c] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []GenreTreeNode
	var loose []Genre
	for _, g := range all { // all уже по убыванию числа книг
		switch {
		case len(kids[g.ID]) > 0:
			out = append(out, GenreTreeNode{Genre: g, Children: kids[g.ID]})
		case !isChild[g.ID]:
			loose = append(loose, g)
		}
	}
	for i := range out {
		sortGenres(out[i].Children)
		out[i].Total = out[i].Count
		for _, c := range out[i].Children {
			out[i].Total += c.Count
		}
	}
	// сверху разделы, где есть свои книги; дальше — порядок справочника по алфавиту
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return foldKey(out[i].Name) < foldKey(out[j].Name)
	})
	if len(loose) > 0 {
		out = append(out, GenreTreeNode{Children: loose})
	}
	return out, nil
}

// sortGenres — сначала с книгами (больше — выше), потом остальные по алфавиту.
func sortGenres(gs []Genre) {
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].Count != gs[j].Count {
			return gs[i].Count > gs[j].Count
		}
		return foldKey(gs[i].Name) < foldKey(gs[j].Name)
	})
}

// EditionGenres — жанры издания по порядку.
func (s *Store) EditionGenres(id int64) ([]Genre, error) {
	rows, err := s.db.Query(`SELECT g.id, g.name FROM edition_genre eg JOIN genre g ON g.id = eg.genre_id
		WHERE eg.edition_id = ? ORDER BY eg.position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Genre
	for rows.Next() {
		var g Genre
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Genres — все жанры: сначала самые наполненные.
func (s *Store) Genres() ([]Genre, error) {
	rows, err := s.db.Query(`SELECT g.id, g.name, COUNT(eg.edition_id) FROM genre g
		LEFT JOIN edition_genre eg ON eg.genre_id = g.id GROUP BY g.id ORDER BY COUNT(eg.edition_id) DESC, fold(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Genre
	for rows.Next() {
		var g Genre
		if err := rows.Scan(&g.ID, &g.Name, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GenreName — название жанра по id.
func (s *Store) GenreName(id int64) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM genre WHERE id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}
