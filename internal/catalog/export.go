package catalog

// Списки для выгрузки каталога (пакет export): всё, что есть, без страниц и отборов.

// EditionIDs — id всех изданий по порядку заведения.
func (s *Store) EditionIDs() ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM edition ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PeopleInfo — справки о людях, которые кто-то заполнил (годы жизни, пара строк, ссылка).
func (s *Store) PeopleInfo() ([]PersonInfo, error) {
	rows, err := s.db.Query(`SELECT name, life, about, link FROM person ORDER BY fold(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonInfo
	for rows.Next() {
		var p PersonInfo
		if err := rows.Scan(&p.Name, &p.Life, &p.About, &p.Link); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
