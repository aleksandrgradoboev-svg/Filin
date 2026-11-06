package livelib

import (
	"fmt"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
)

// Result — итог импорта одного файла.
type Result struct {
	Added, Skipped, Copies, Covers int
	Problems                       []string // книги, у которых что-то не легло; сами книги сохранены
}

// Import заводит книги в каталог. Книга, уже импортированная раньше (тот же id
// LiveLib), пропускается: файл можно импортировать повторно. Книга со значком
// «Мои книги» получает экземпляр без полки — полку человек укажет сам.
func Import(st *catalog.Store, covers cover.Dir, books []Book) (Result, error) {
	var r Result
	for _, b := range books {
		if id, err := st.EditionByLiveLibID(b.ID); err != nil {
			return r, err
		} else if id != 0 {
			r.Skipped++
			continue
		}
		e := catalog.Edition{Title: b.Title, Source: "livelib", SourceURL: b.URL, LiveLibID: b.ID}
		for _, a := range b.Authors {
			e.People = append(e.People, catalog.Person{Name: a})
		}
		id, err := st.AddEdition(&e)
		if err != nil {
			return r, fmt.Errorf("%s: %w", b.Title, err)
		}
		r.Added++
		if b.Mine {
			if _, err := st.AddCopy(&catalog.Copy{EditionID: id}); err != nil {
				return r, fmt.Errorf("%s: %w", b.Title, err)
			}
			r.Copies++
		}
		if b.Cover == nil {
			continue
		}
		jpeg, err := cover.Process(b.Cover)
		if err == nil {
			err = covers.Replace(id, jpeg, func(name string) (string, error) { return st.SetCover(id, name) })
		}
		if err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("%s: обложка — %v", b.Title, err))
			continue
		}
		r.Covers++
	}
	return r, nil
}
