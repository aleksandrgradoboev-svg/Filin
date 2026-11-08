//go:build !windows

package paddle

import "errors"

// Филин собирается под Windows; на прочих системах распознавание PaddleOCR не подключено.

type runtimeORT struct{}

type session struct{}

func loadORT(string) (*runtimeORT, error) {
	return nil, errors.New("распознавание PaddleOCR есть только в сборке для Windows")
}

func (r *runtimeORT) newSession([]byte, string, string, int) (*session, error) { return nil, nil }

func (s *session) close() {}

func (s *session) run([]float32, []int64) ([]float32, []int64, error) { return nil, nil, nil }
