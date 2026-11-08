package ocr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Samples — каталог проб распознавания: на каждое фото титула папка с самим фото,
// выводом Tesseract, черновиком и тем, что человек в итоге сохранил. По ним видно,
// сколько полей угадано, и разбор можно переиграть по выводу Tesseract после правки правил.
//
// Фото — почти весь вес пробы (~2 МБ): когда книга сохранена, оно удаляется, текст остаётся
// (решено 08.11.2026). Без фото пропадает лишь возможность распознать заново другими настройками.
type Samples string

// samplePhoto — фото титула в папке пробы.
const samplePhoto = "photo.jpg"

var sampleRe = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{6}$`)

// ErrBadSample — номер пробы не нашего вида (из запроса пришло что-то чужое).
var ErrBadSample = errors.New("неизвестная проба распознавания")

// Save кладёт пробу и возвращает её номер.
func (s Samples) Save(photo, tsv []byte, d Draft) (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
	dir := filepath.Join(string(s), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, samplePhoto), photo, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "ocr.tsv"), tsv, 0o644); err != nil {
		return "", err
	}
	return id, writeJSON(filepath.Join(dir, "draft.json"), d)
}

// Load читает черновик пробы.
func (s Samples) Load(id string) (Draft, error) {
	var d Draft
	dir, err := s.dir(id)
	if err != nil {
		return d, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil {
		return d, ErrBadSample
	}
	return d, json.Unmarshal(data, &d)
}

// Finish записывает к пробе итог: что сохранено в карточке и под каким номером книги, —
// и убирает фото: книга сохранена, текст пробы остаётся.
func (s Samples) Finish(id string, final any) error {
	dir, err := s.dir(id)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "final.json"), final); err != nil {
		return fmt.Errorf("итог не записан: %w", err) // фото не трогаем: проба ещё не закончена
	}
	if err := dropPhoto(dir); err != nil {
		return fmt.Errorf("итог записан, фото не удалено (уберётся при следующем запуске): %w", err)
	}
	return nil
}

// DropFinishedPhotos убирает фото у проб, книги которых уже сохранены (есть final.json),
// — для проб, заведённых до правила «фото удалять». Возвращает, сколько фото убрано.
func (s Samples) DropFinishedPhotos() (int, error) {
	ents, err := os.ReadDir(string(s))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil // проб ещё не было
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() || !sampleRe.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(string(s), e.Name())
		if _, err := os.Stat(filepath.Join(dir, "final.json")); err != nil {
			continue // книга не сохранена — проба может быть ещё в работе
		}
		if _, err := os.Stat(filepath.Join(dir, samplePhoto)); err != nil {
			continue
		}
		if err := dropPhoto(dir); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func dropPhoto(dir string) error {
	if err := os.Remove(filepath.Join(dir, samplePhoto)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s Samples) dir(id string) (string, error) {
	if !sampleRe.MatchString(id) {
		return "", ErrBadSample
	}
	dir := filepath.Join(string(s), id)
	if _, err := os.Stat(dir); err != nil {
		return "", ErrBadSample
	}
	return dir, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
