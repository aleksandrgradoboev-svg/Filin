package main

import (
	"net/url"
	"runtime"
	"strings"
)

// issuesURL — обращения к Филину: канал поддержки выбран 15.11.2026 — GitHub Issues
// публичного репозитория.
const issuesURL = "https://github.com/aleksandrgradoboev-svg/Filin/issues/new"

// reportURL — новое обращение с уже подставленной версией: первый вопрос к любой ошибке
// «на какой версии», и человек не должен искать ответ сам.
func reportURL(version string) string {
	body := "**Версия Филина:** " + version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")\n\n" +
		"**Что делали:**\n\n\n" +
		"**Что ожидали:**\n\n\n" +
		"**Что получилось:**\n\n\n" +
		"<!-- Журнал filin.log лежит рядом с базой. Его можно приложить, но сначала просмотрите: " +
		"там есть пути к папкам и адреса домашней сети. -->\n"
	// пробел — %20, а не «+»: «+» как пробел понимает не всякий разбор адреса, а настоящий
	// «+» в тексте Encode уже превратил в %2B
	return issuesURL + "?" + strings.ReplaceAll(url.Values{"body": {body}}.Encode(), "+", "%20")
}
