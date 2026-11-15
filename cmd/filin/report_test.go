package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestReportURL(t *testing.T) {
	u, err := url.Parse(reportURL("1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "github.com" || u.Path != "/aleksandrgradoboev-svg/Filin/issues/new" {
		t.Fatalf("адрес: %s", u)
	}
	body := u.Query().Get("body")
	if !strings.Contains(body, "**Версия Филина:** 1.2.3 (") {
		t.Errorf("версия не подставлена: %q", body)
	}
	if strings.ContainsAny(u.RawQuery, " \n+") {
		t.Errorf("запрос не закодирован: %q", u.RawQuery)
	}
}
