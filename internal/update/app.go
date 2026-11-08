package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Приложение для телефона — отдельными файлами выпуска (filin.apk и app.json с номером
// версии), а не внутри filin.exe: оно меняется реже программы и весило две трети каждого
// обновления. Филин при проверке обновлений сам кладёт новое приложение в свой каталог
// приложения, откуда его берёт телефон (/app.apk).

// File — файл выпуска: где лежит, сумма и размер.
type File struct {
	URL    string
	SHA256 string
	Size   int64
}

// Имена файлов приложения в выпуске и в каталоге приложения.
const (
	AppAPK  = "filin.apk"
	AppInfo = "app.json"
)

// appVersion — номер версии из app.json; нет файла или он битый — 0.
func appVersion(b []byte) int {
	var a struct {
		VersionCode int `json:"version_code"`
	}
	if json.Unmarshal(b, &a) != nil {
		return 0
	}
	return a.VersionCode
}

// SyncApp кладёт в appDir приложение из выпуска, если оно новее того, что там лежит.
// Порядок — как у android/publish.ps1: сначала целый APK (с докачкой и сверкой суммы), потом
// app.json — пока сведения старые, телефон за новым файлом не полезет. Возвращает номер
// версии, которая теперь лежит в appDir (0 — нечего было класть).
func SyncApp(ctx context.Context, c *http.Client, r Release, appDir, ua string) (int, error) {
	if r.APK.URL == "" || r.AppInfo.URL == "" {
		return 0, nil // выпуск без приложения отдельными файлами (старые выпуски)
	}
	info, err := fetchSmall(ctx, c, r.AppInfo.URL, ua)
	if err != nil {
		return 0, fmt.Errorf("сведения о приложении: %w", err)
	}
	remote := appVersion(info)
	if remote <= 0 {
		return 0, fmt.Errorf("в %s выпуска %s нет номера версии", AppInfo, r.Version)
	}
	localInfo, _ := os.ReadFile(filepath.Join(appDir, AppInfo))
	local := appVersion(localInfo)
	if _, err := os.Stat(filepath.Join(appDir, AppAPK)); err != nil {
		local = 0 // сведения есть, а файла нет — класть заново
	}
	if remote <= local {
		return local, nil
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return 0, err
	}
	if err := Fetch(ctx, c, r.APK.URL, filepath.Join(appDir, AppAPK), r.APK.Size, r.APK.SHA256, ua, nil); err != nil {
		return 0, fmt.Errorf("приложение для телефона: %w", err)
	}
	tmp := filepath.Join(appDir, AppInfo+".new")
	if err := os.WriteFile(tmp, info, 0o644); err != nil {
		return 0, err
	}
	os.Remove(filepath.Join(appDir, AppInfo))
	if err := os.Rename(tmp, filepath.Join(appDir, AppInfo)); err != nil {
		return 0, err
	}
	return remote, nil
}

// fetchSmall — маленький файл целиком (app.json): не больше 64 КБ.
func fetchSmall(ctx context.Context, c *http.Client, url, ua string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер ответил %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}
