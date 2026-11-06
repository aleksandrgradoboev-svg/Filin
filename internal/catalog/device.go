package catalog

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Device — привязанный телефон.
type Device struct {
	ID        int64
	Name      string
	CreatedAt string
	LastSeen  string // пусто — ещё не заходил
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// AddDevice заводит устройство и возвращает его токен. Токен показывается один раз:
// в базе только хэш.
func (s *Store) AddDevice(name string) (token string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "телефон"
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token = hex.EncodeToString(b)
	_, err = s.db.Exec(`INSERT INTO device (name, token_hash) VALUES (?, ?)`, name, hashToken(token))
	return token, err
}

// DeviceByToken — устройство по токену; заодно отмечает, когда оно заходило.
// Неизвестный или отозванный токен — ErrNotFound.
func (s *Store) DeviceByToken(token string) (Device, error) {
	var d Device
	if token == "" {
		return d, ErrNotFound
	}
	h := hashToken(token)
	var seen sql.NullString
	err := s.db.QueryRow(`SELECT id, name, created_at, last_seen FROM device WHERE token_hash = ?`, h).
		Scan(&d.ID, &d.Name, &d.CreatedAt, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.LastSeen = seen.String
	// Отметка — не чаще раза в минуту: страница в приложении тянет десятки обложек,
	// и запись в базу на каждую из них — лишние блокировки ради одной даты.
	if !seen.Valid || seen.String < time.Now().UTC().Add(-time.Minute).Format("2006-01-02T15:04:05Z") {
		_, err = s.db.Exec(`UPDATE device SET last_seen = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = ?`, d.ID)
	}
	return d, err
}

func (s *Store) Devices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, COALESCE(last_seen, '') FROM device ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice отзывает доступ: токен устройства перестаёт работать сразу.
func (s *Store) RevokeDevice(id int64) error {
	_, err := s.db.Exec(`DELETE FROM device WHERE id = ?`, id)
	return err
}
