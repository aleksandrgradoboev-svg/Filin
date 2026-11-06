package cover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Dir — каталог файлов обложек. В базе хранится только имя файла.
type Dir string

// validName — имена, которые выдаёт Save: «id-время.jpg». Всё прочее — не обложка,
// в том числе попытка выйти из каталога через «..».
var validName = regexp.MustCompile(`^[0-9]+-[0-9]+\.jpg$`)

func ValidName(name string) bool { return validName.MatchString(name) }

// Save пишет готовую обложку издания id и возвращает имя файла. Имя с отметкой
// времени: браузер кэширует обложку надолго, новое имя — новая картинка.
func (d Dir) Save(id int64, jpeg []byte) (string, error) {
	if err := os.MkdirAll(string(d), 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d-%d.jpg", id, time.Now().UnixNano())
	return name, os.WriteFile(d.Path(name), jpeg, 0o644)
}

// Remove убирает файл; отсутствующий — не ошибка.
func (d Dir) Remove(name string) error {
	// имя приходит из базы, а база могла прийти из чужого архива копии: «..\..\файл» —
	// не обложка, удалять такое нельзя
	if name == "" || !ValidName(name) {
		return nil
	}
	if err := os.Remove(d.Path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d Dir) Path(name string) string { return filepath.Join(string(d), name) }

// Replace ставит изданию id новую обложку (jpeg == nil — снимает) и убирает старый файл.
// set записывает имя в базу и возвращает прежнее. Не записалось в базу — новый файл
// удаляется: файл без ссылки из базы — мусор.
func (d Dir) Replace(id int64, jpeg []byte, set func(name string) (old string, err error)) error {
	name := ""
	if jpeg != nil {
		var err error
		if name, err = d.Save(id, jpeg); err != nil {
			return err
		}
	}
	old, err := set(name)
	if err != nil {
		d.Remove(name)
		return err
	}
	if old != name {
		d.Remove(old) // не убрался — лишний файл, но обложка уже стоит: не ошибка
	}
	return nil
}
