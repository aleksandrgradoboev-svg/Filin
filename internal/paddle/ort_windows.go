//go:build windows

package paddle

// Тонкая обвязка C API ONNX Runtime через syscall — без CGO, как и весь Филин.
// Таблица функций OrtApi только дополняется от версии к версии, поэтому номера ниже
// одинаковы для ONNX Runtime 1.23 и новее; запрашивается таблица версии 23.
// Номера сверены с github.com/younland/onnxruntime (api.go, MIT, сгенерирован по
// заголовкам 1.23–1.31) и проверены вызовами на onnxruntime.dll 1.31.1.
//
// Всё, чей адрес уходит в ONNX Runtime, лежит в куче (поля структур, созданных new/make):
// стек горутины при росте переезжает, и адрес переменной со стека, переданный дальше
// обычной функцией, а не прямо в syscall.SyscallN, к моменту вызова указывал бы в пустоту.
// Куча в Go не перемещается.

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const ortAPIVersion = 23

const (
	fnGetErrorMessage                  = 2
	fnCreateEnv                        = 3
	fnCreateSessionFromArray           = 8
	fnRun                              = 9
	fnCreateSessionOptions             = 10
	fnSetSessionGraphOptimizationLevel = 23
	fnSetIntraOpNumThreads             = 24
	fnCreateTensorWithDataAsOrtValue   = 49
	fnGetTensorMutableData             = 51
	fnGetDimensionsCount               = 61
	fnGetDimensions                    = 62
	fnGetTensorTypeAndShape            = 65
	fnCreateCpuMemoryInfo              = 69
	fnReleaseEnv                       = 92
	fnReleaseStatus                    = 93
	fnReleaseMemoryInfo                = 94
	fnReleaseSession                   = 95
	fnReleaseValue                     = 96
	fnReleaseTensorTypeAndShapeInfo    = 99
	fnReleaseSessionOptions            = 100
)

const (
	ortLoggingLevelError = 3
	ortEnableAll         = 99 // GraphOptimizationLevel
	ortArenaAllocator    = 1
	ortMemTypeDefault    = 0
	ortTensorFloat       = 1 // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT
)

// runtimeORT — загруженная библиотека: таблица функций, окружение и описание памяти.
type runtimeORT struct {
	api     uintptr
	env     uintptr
	memInfo uintptr
	name    *byte

	mu   sync.Mutex // newSession: opts — общее поле
	opts uintptr
}

var (
	loadOnce sync.Once
	loaded   *runtimeORT
	loadErr  error
)

// loadORT загружает onnxruntime.dll один раз на процесс: выгружать её нельзя —
// ONNX Runtime при выгрузке падает в своих деструкторах.
func loadORT(dll string) (*runtimeORT, error) {
	loadOnce.Do(func() { loaded, loadErr = openORT(dll) })
	return loaded, loadErr
}

func openORT(dll string) (*runtimeORT, error) {
	h, err := syscall.LoadLibrary(dll)
	if err != nil {
		return nil, fmt.Errorf("распознавание: не загрузилась %s: %w", dll, err)
	}
	base, err := syscall.GetProcAddress(h, "OrtGetApiBase")
	if err != nil {
		return nil, fmt.Errorf("распознавание: в %s нет OrtGetApiBase: %w", dll, err)
	}
	apiBase, _, _ := syscall.SyscallN(base)
	if apiBase == 0 {
		return nil, errors.New("распознавание: OrtGetApiBase вернул пусто")
	}
	getAPI := *(*uintptr)(unsafe.Pointer(apiBase)) //nolint:govet // адрес из библиотеки, не из Go
	api, _, _ := syscall.SyscallN(getAPI, ortAPIVersion)
	if api == 0 {
		return nil, fmt.Errorf("распознавание: ONNX Runtime старше 1.%d", ortAPIVersion)
	}
	r := &runtimeORT{api: api}
	r.name, _ = syscall.BytePtrFromString("filin")
	if err := r.call(fnCreateEnv, ortLoggingLevelError, uintptr(unsafe.Pointer(r.name)), uintptr(unsafe.Pointer(&r.env))); err != nil {
		return nil, err
	}
	if err := r.call(fnCreateCpuMemoryInfo, ortArenaAllocator, ortMemTypeDefault, uintptr(unsafe.Pointer(&r.memInfo))); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *runtimeORT) fn(i int) uintptr {
	return *(*uintptr)(unsafe.Pointer(r.api + uintptr(i)*unsafe.Sizeof(uintptr(0)))) //nolint:govet // таблица ONNX Runtime
}

// call — вызов функции, возвращающей OrtStatus*: не пусто — ошибка с текстом ONNX Runtime.
func (r *runtimeORT) call(i int, args ...uintptr) error {
	st, _, _ := syscall.SyscallN(r.fn(i), args...)
	if st == 0 {
		return nil
	}
	msg, _, _ := syscall.SyscallN(r.fn(fnGetErrorMessage), st)
	text := cString(msg)
	syscall.SyscallN(r.fn(fnReleaseStatus), st)
	return fmt.Errorf("ONNX Runtime: %s", text)
}

func (r *runtimeORT) release(i int, h uintptr) {
	if h != 0 {
		syscall.SyscallN(r.fn(i), h)
	}
}

func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for {
		c := *(*byte)(unsafe.Pointer(p)) //nolint:govet // строка ONNX Runtime
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
		p++
	}
}

// session — загруженная модель с одним входом и одним выходом (так устроены модели PaddleOCR).
type session struct {
	rt    *runtimeORT
	h     uintptr
	model []byte // ONNX Runtime копирует модель, но держим до закрытия — на всякий случай

	mu      sync.Mutex // одна сессия — один запуск за раз; поля ниже — места для ответов ONNX Runtime
	in      [1]*byte
	out     [1]*byte
	inputs  [1]uintptr
	output  uintptr
	shape   [8]int64
	info    uintptr
	ndims   uintptr
	dataPtr uintptr
}

func (r *runtimeORT) newSession(model []byte, inName, outName string, threads int) (*session, error) {
	s := &session{rt: r, model: model}
	s.in[0], _ = syscall.BytePtrFromString(inName)
	s.out[0], _ = syscall.BytePtrFromString(outName)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts = 0
	if err := r.call(fnCreateSessionOptions, uintptr(unsafe.Pointer(&r.opts))); err != nil {
		return nil, err
	}
	defer r.release(fnReleaseSessionOptions, r.opts)
	if err := r.call(fnSetSessionGraphOptimizationLevel, r.opts, ortEnableAll); err != nil {
		return nil, err
	}
	if threads > 0 {
		if err := r.call(fnSetIntraOpNumThreads, r.opts, uintptr(threads)); err != nil {
			return nil, err
		}
	}
	if err := r.call(fnCreateSessionFromArray, r.env, uintptr(unsafe.Pointer(&s.model[0])), uintptr(len(s.model)), r.opts,
		uintptr(unsafe.Pointer(&s.h))); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *session) close() {
	if s != nil && s.h != 0 {
		s.rt.release(fnReleaseSession, s.h)
		s.h = 0
	}
}

// run — один прогон: вход float32 формы shape (до 8 измерений), выход — копия данных и форма.
// data должен лежать в куче (make с размером, известным только при работе, — так и есть).
func (s *session) run(data []float32, shape []int64) ([]float32, []int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rt
	if len(shape) > len(s.shape) || len(data) == 0 {
		return nil, nil, errors.New("распознавание: неверная форма входа")
	}
	copy(s.shape[:], shape)
	s.inputs[0] = 0
	if err := r.call(fnCreateTensorWithDataAsOrtValue, r.memInfo, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*4),
		uintptr(unsafe.Pointer(&s.shape[0])), uintptr(len(shape)), ortTensorFloat, uintptr(unsafe.Pointer(&s.inputs[0]))); err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseValue, s.inputs[0])
	s.output = 0
	err := r.call(fnRun, s.h, 0, uintptr(unsafe.Pointer(&s.in[0])), uintptr(unsafe.Pointer(&s.inputs[0])), 1,
		uintptr(unsafe.Pointer(&s.out[0])), 1, uintptr(unsafe.Pointer(&s.output)))
	runtime.KeepAlive(data)
	if err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseValue, s.output)

	s.info = 0
	if err := r.call(fnGetTensorTypeAndShape, s.output, uintptr(unsafe.Pointer(&s.info))); err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseTensorTypeAndShapeInfo, s.info)
	if err := r.call(fnGetDimensionsCount, s.info, uintptr(unsafe.Pointer(&s.ndims))); err != nil {
		return nil, nil, err
	}
	if s.ndims == 0 || s.ndims > uintptr(len(s.shape)) {
		return nil, nil, fmt.Errorf("распознавание: у ответа модели %d измерений", s.ndims)
	}
	if err := r.call(fnGetDimensions, s.info, uintptr(unsafe.Pointer(&s.shape[0])), s.ndims); err != nil {
		return nil, nil, err
	}
	dims := append([]int64(nil), s.shape[:s.ndims]...)
	total := int64(1)
	for _, d := range dims {
		total *= d
	}
	if err := r.call(fnGetTensorMutableData, s.output, uintptr(unsafe.Pointer(&s.dataPtr))); err != nil {
		return nil, nil, err
	}
	out := make([]float32, total)
	if total > 0 {
		copy(out, unsafe.Slice((*float32)(unsafe.Pointer(s.dataPtr)), total)) //nolint:govet // память ONNX Runtime
	}
	return out, dims, nil
}
