package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneCopies(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	files := map[string]time.Duration{
		"filin-перед-v0.6.1-2026-11-01.zip": 3 * time.Hour,
		"filin-перед-v0.6.2-2026-11-05.zip": 2 * time.Hour,
		"filin-перед-v0.6.3-2026-11-08.zip": time.Hour,
		"filin-2026-11-01.zip":              10 * time.Hour, // скачана кнопкой «Копия» — не наша
		"filin.db":                          10 * time.Hour,
	}
	for name, age := range files {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, now.Add(-age), now.Add(-age))
	}
	pruneCopies(dir, 2)
	for name := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		gone := os.IsNotExist(err)
		if want := name == "filin-перед-v0.6.1-2026-11-01.zip"; gone != want {
			t.Errorf("%s: удалён = %v, want %v", name, gone, want)
		}
	}
}

// TestCloseWaitsForSwap — закрытие программы дожидается начатой замены, а после закрытия
// новая не начинается: иначе выход между двумя переименованиями оставил бы папку без filin.exe.
func TestCloseWaitsForSwap(t *testing.T) {
	u := &updater{}
	u.applying.Add(1)
	closed := make(chan struct{})
	go func() { u.close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("close не дождался замены")
	case <-time.After(50 * time.Millisecond):
	}
	u.applying.Done()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close не вернулся после замены")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.closing {
		t.Fatal("после close замены не запрещены")
	}
}

func TestSupportedWithoutFlags(t *testing.T) {
	// main не запускался, флаги не разобраны: выключено, а не паника
	if supported() {
		t.Fatal("без адреса выпусков обновление считается включённым")
	}
}
