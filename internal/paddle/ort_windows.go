//go:build windows

package paddle

// Тонкая обвязка C API ONNX Runtime через syscall — без CGO, как и весь Филин.
// Таблица функций OrtApi только дополняется от версии к версии, поэтому номера ниже
// одинаковы для ONNX Runtime 1.23 и новее; запрашивается таблица версии 23.
// Номера сверены с github.com/younland/onnxruntime (api.go, MIT, сгенерирован по
// заголовкам 1.23–1.31) и проверены вызовами на onnxruntime.dll 1.31.1.

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
	getAPI := *(*uintptr)(unsafe.Pointer(apiBase)) // OrtApiBase.GetApi — первое поле
	api, _, _ := syscall.SyscallN(getAPI, ortAPIVersion)
	if api == 0 {
		return nil, fmt.Errorf("распознавание: ONNX Runtime старше 1.%d", ortAPIVersion)
	}
	r := &runtimeORT{api: api}
	name, _ := syscall.BytePtrFromString("filin")
	if err := r.call(fnCreateEnv, ortLoggingLevelError, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&r.env))); err != nil {
		return nil, err
	}
	runtime.KeepAlive(name)
	if err := r.call(fnCreateCpuMemoryInfo, ortArenaAllocator, ortMemTypeDefault, uintptr(unsafe.Pointer(&r.memInfo))); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *runtimeORT) fn(i int) uintptr {
	return *(*uintptr)(unsafe.Pointer(r.api + uintptr(i)*unsafe.Sizeof(uintptr(0))))
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
		c := *(*byte)(unsafe.Pointer(p))
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
		p++
	}
}

// session — загруженная модель с одним входом и одним выходом (так устроены модели PaddleOCR).
type session struct {
	rt      *runtimeORT
	h       uintptr
	in, out *byte
	inName  string
	outName string
	mu      sync.Mutex // одна сессия — один запуск за раз: так проще и памяти меньше
}

func (r *runtimeORT) newSession(model []byte, inName, outName string, threads int) (*session, error) {
	var opts uintptr
	if err := r.call(fnCreateSessionOptions, uintptr(unsafe.Pointer(&opts))); err != nil {
		return nil, err
	}
	defer r.release(fnReleaseSessionOptions, opts)
	if err := r.call(fnSetSessionGraphOptimizationLevel, opts, ortEnableAll); err != nil {
		return nil, err
	}
	if threads > 0 {
		if err := r.call(fnSetIntraOpNumThreads, opts, uintptr(threads)); err != nil {
			return nil, err
		}
	}
	s := &session{rt: r, inName: inName, outName: outName}
	if err := r.call(fnCreateSessionFromArray, r.env, uintptr(unsafe.Pointer(&model[0])), uintptr(len(model)), opts,
		uintptr(unsafe.Pointer(&s.h))); err != nil {
		return nil, err
	}
	runtime.KeepAlive(model)
	s.in, _ = syscall.BytePtrFromString(inName)
	s.out, _ = syscall.BytePtrFromString(outName)
	return s, nil
}

func (s *session) close() {
	if s != nil {
		s.rt.release(fnReleaseSession, s.h)
		s.h = 0
	}
}

// run — один прогон: вход float32 формы shape, выход — копия данных и форма.
func (s *session) run(data []float32, shape []int64) ([]float32, []int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rt
	var input uintptr
	if err := r.call(fnCreateTensorWithDataAsOrtValue, r.memInfo, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*4),
		uintptr(unsafe.Pointer(&shape[0])), uintptr(len(shape)), ortTensorFloat, uintptr(unsafe.Pointer(&input))); err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseValue, input)
	var output uintptr
	inNames, outNames := []*byte{s.in}, []*byte{s.out}
	inputs := []uintptr{input}
	err := r.call(fnRun, s.h, 0, uintptr(unsafe.Pointer(&inNames[0])), uintptr(unsafe.Pointer(&inputs[0])), 1,
		uintptr(unsafe.Pointer(&outNames[0])), 1, uintptr(unsafe.Pointer(&output)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(shape)
	runtime.KeepAlive(inNames)
	runtime.KeepAlive(outNames)
	runtime.KeepAlive(inputs)
	if err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseValue, output)

	var info uintptr
	if err := r.call(fnGetTensorTypeAndShape, output, uintptr(unsafe.Pointer(&info))); err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseTensorTypeAndShapeInfo, info)
	var n uintptr
	if err := r.call(fnGetDimensionsCount, info, uintptr(unsafe.Pointer(&n))); err != nil {
		return nil, nil, err
	}
	dims := make([]int64, n)
	if n > 0 {
		if err := r.call(fnGetDimensions, info, uintptr(unsafe.Pointer(&dims[0])), n); err != nil {
			return nil, nil, err
		}
	}
	total := int64(1)
	for _, d := range dims {
		total *= d
	}
	var p uintptr
	if err := r.call(fnGetTensorMutableData, output, uintptr(unsafe.Pointer(&p))); err != nil {
		return nil, nil, err
	}
	out := make([]float32, total)
	if total > 0 {
		copy(out, unsafe.Slice((*float32)(unsafe.Pointer(p)), total))
	}
	return out, dims, nil
}
