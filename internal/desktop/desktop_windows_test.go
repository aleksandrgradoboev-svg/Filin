package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// Ярлык создаётся там, где сказано, с кавычками в аргументах и апострофом в пути.
func TestMakeShortcut(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "папка с 'апострофом'")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lnk := filepath.Join(dir, "Филин.lnk")
	icon := filepath.Join(dir, "filin.ico")
	os.WriteFile(icon, Icon, 0o644)
	exe, _ := os.Executable()
	if err := MakeShortcut(lnk, exe, `-db "C:\Мои книги\filin.db" app`, dir, icon); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(lnk); err != nil || st.Size() == 0 {
		t.Fatalf("ярлыка нет: %v", err)
	}
}

func TestHomeIP(t *testing.T) {
	if ip := HomeIP(); ip != nil && !ip.IsPrivate() {
		t.Errorf("наружу выставлен не домашний адрес: %s", ip)
	}
}

// Tesseract ставится в Program Files и в PATH себя не прописывает — Филин находит его сам.
func TestFindTesseract(t *testing.T) {
	pf := t.TempDir()
	t.Setenv("PATH", "")
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("ProgramFiles", pf)
	if got := FindTesseract(); got != "" {
		t.Errorf("нашёлся несуществующий: %q", got)
	}
	os.MkdirAll(filepath.Join(pf, "Tesseract-OCR"), 0o755)
	want := filepath.Join(pf, "Tesseract-OCR", "tesseract.exe")
	os.WriteFile(want, []byte("MZ"), 0o755)
	if got := FindTesseract(); got != want {
		t.Errorf("Tesseract в Program Files: %q", got)
	}
}

// Ярлык делается и без PowerShell в PATH — так запускает .cmd проводник.
func TestMakeShortcutWithoutPath(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	lnk := filepath.Join(dir, "Филин.lnk")
	exe, _ := os.Executable()
	if err := MakeShortcut(lnk, exe, "app", dir, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lnk); err != nil {
		t.Fatalf("ярлыка нет: %v", err)
	}
}
