package ocr

import (
	"os"
	"path/filepath"
	"testing"
)

// Фото пробы — почти весь её вес: книга сохранена — фото убирается, текст остаётся.
func TestFinishDropsPhotoKeepsText(t *testing.T) {
	s := Samples(t.TempDir())
	id, err := s.Save([]byte("фото"), []byte("tsv"), Draft{Title: "Солдат"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(string(s), id)
	if err := s.Finish(id, map[string]any{"book": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, samplePhoto)); !os.IsNotExist(err) {
		t.Error("фото осталось после сохранения книги")
	}
	for _, f := range []string{"ocr.tsv", "draft.json", "final.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("текст пробы пропал: %s", f)
		}
	}
	if d, err := s.Load(id); err != nil || d.Title != "Солдат" {
		t.Errorf("черновик не читается: %+v %v", d, err)
	}
}

// Пробы до правила: фото убирается только у сохранённых книг; незаконченная проба — с фото.
func TestDropFinishedPhotos(t *testing.T) {
	s := Samples(t.TempDir())
	done, _ := s.Save([]byte("фото"), []byte("tsv"), Draft{})
	open, _ := s.Save([]byte("фото"), []byte("tsv"), Draft{})
	// «старая» сохранённая проба: итог есть, фото ещё лежит (как до правила)
	writeJSON(filepath.Join(string(s), done, "final.json"), map[string]any{"book": 1})
	os.MkdirAll(filepath.Join(string(s), "чужая-папка"), 0o755)
	os.WriteFile(filepath.Join(string(s), "чужая-папка", samplePhoto), []byte("x"), 0o644)

	n, err := s.DropFinishedPhotos()
	if err != nil || n != 1 {
		t.Fatalf("убрано %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(string(s), done, samplePhoto)); !os.IsNotExist(err) {
		t.Error("у сохранённой книги фото осталось")
	}
	if _, err := os.Stat(filepath.Join(string(s), open, samplePhoto)); err != nil {
		t.Error("у незаконченной пробы фото удалено")
	}
	if _, err := os.Stat(filepath.Join(string(s), "чужая-папка", samplePhoto)); err != nil {
		t.Error("тронута папка не пробы")
	}
	if n, _ := s.DropFinishedPhotos(); n != 0 {
		t.Errorf("повторный проход убрал ещё %d", n)
	}
	if n, err := Samples(filepath.Join(t.TempDir(), "нет")).DropFinishedPhotos(); n != 0 || err != nil {
		t.Errorf("проб ещё не было: %d %v", n, err)
	}
}
