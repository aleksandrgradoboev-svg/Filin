package catalog

import (
	"database/sql"
	"strings"
)

// Темы — о чём книга (жанр — что это за книга). Тема — цепочка уровней от общего к частному,
// как тематическая рубрика РГБ: «История -- СССР -- Период гражданской войны (1918-1920)».

// SubjectSep — разделитель уровней темы в базе и в форме: такой же, как в записях РГБ.
const SubjectSep = " -- "

// SubjectLevels — уровни темы: «История -- СССР» → [История, СССР]. Пустые уровни отброшены.
func SubjectLevels(path string) []string {
	var out []string
	for _, l := range strings.Split(path, "--") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ParseSubjects — темы из поля формы: по теме в строке, уровни через «--».
// Пробелы приводятся к одному виду, повторы и пустые строки отброшены.
func ParseSubjects(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		p := strings.Join(SubjectLevels(line), SubjectSep)
		if k := foldKey(p); p != "" && !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

func writeSubjects(tx *sql.Tx, id int64, subjects []string) error {
	if _, err := tx.Exec(`DELETE FROM edition_subject WHERE edition_id = ?`, id); err != nil {
		return err
	}
	for i, p := range ParseSubjects(strings.Join(subjects, "\n")) {
		if _, err := tx.Exec(`INSERT INTO edition_subject (edition_id, position, path) VALUES (?, ?, ?)`,
			id, i+1, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) editionSubjects(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM edition_subject WHERE edition_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// subjectMatch — условие «у издания e есть тема с уровнем ?»: уровень совпадает целиком
// (без учёта регистра и «ё»), на любой глубине цепочки.
const subjectMatch = `e.id IN (SELECT edition_id FROM edition_subject
	WHERE instr(fold(' -- ' || path || ' -- '), fold(' -- ' || ? || ' -- ')) > 0)`
