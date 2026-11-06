// Package bundled — приложение для телефона, вшитое в filin.exe при сборке выпуска.
//
// Человек скачивает с выпуска один filin.exe — и QR «поставить приложение» на странице
// «Телефоны» должен сразу вести к APK. release.ps1 на время сборки кладёт в app/ файлы
// filin.apk и app.json; в обычной (разработческой) сборке их нет, и App сообщает об этом.
package bundled

import (
	"embed"
	"io/fs"
)

//go:embed all:app
var files embed.FS

// App — вшитые app.json и filin.apk; ok=false — сборка без приложения.
func App() (info, apk []byte, ok bool) {
	info, err1 := fs.ReadFile(files, "app/app.json")
	apk, err2 := fs.ReadFile(files, "app/filin.apk")
	if err1 != nil || err2 != nil || len(apk) == 0 {
		return nil, nil, false
	}
	return info, apk, true
}
