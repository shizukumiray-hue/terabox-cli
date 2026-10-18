package terabox

import (
	"fmt"
	"sync"
	"time"

	"github.com/sony/gobreaker/v2"
	"go.uber.org/zap"
)

var loggerPtr *zap.Logger

func init() {
	// Default zap logger jika belum di-set
	l, err := zap.NewProduction()
	if err == nil {
		loggerPtr = l
	}
}

func SetLogger(l *zap.Logger) {
	loggerPtr = l
}

func logInfo(msg string, fields ...zap.Field) {
	if loggerPtr != nil {
		loggerPtr.Info(msg, fields...)
	}
}

func logWarn(msg string, fields ...zap.Field) {
	if loggerPtr != nil {
		loggerPtr.Warn(msg, fields...)
	}
}

func logError(msg string, fields ...zap.Field) {
	if loggerPtr != nil {
		loggerPtr.Error(msg, fields...)
	}
}

// BreakerRegistry mengelola circuit breaker untuk masing-masing provider Terabox
type BreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[string]*gobreaker.CircuitBreaker[[]TeraboxUniversalData]
}

var registry = &BreakerRegistry{
	breakers: make(map[string]*gobreaker.CircuitBreaker[[]TeraboxUniversalData]),
}

func (r *BreakerRegistry) Get(name string) *gobreaker.CircuitBreaker[[]TeraboxUniversalData] {
	r.mu.RLock()
	cb, ok := r.breakers[name]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if cb, ok := r.breakers[name]; ok {
		return cb
	}

	st := gobreaker.Settings{
		Name:        name,
		MaxRequests: 1,               // Dalam state HalfOpen izinkan 1 request uji coba
		Interval:    5 * time.Minute, // Siklus reset metrik dalam state Closed
		Timeout:     3 * time.Minute, // Durasi Open sebelum beralih ke HalfOpen
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Trip setelah 3 kali gagal berturut-turut
			return counts.ConsecutiveFailures >= 3
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logWarn("Terabox Circuit Breaker State Transition",
				zap.String("api", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	}

	cb = gobreaker.NewCircuitBreaker[[]TeraboxUniversalData](st)
	r.breakers[name] = cb
	return cb
}

func (r *BreakerRegistry) Reset(name string) {
	r.mu.Lock()
	delete(r.breakers, name)
	r.mu.Unlock()
}

// FetchTeraboxUniversal mengeksekusi sistem berlapis dengan perlindungan circuit breaker gobreaker/v2
func FetchTeraboxUniversal(teraboxURL string) ([]TeraboxUniversalData, error) {
	apis := []struct {
		name string
		fn   func(string) ([]TeraboxUniversalData, error)
	}{
		{"Komiktap API", FetchKomiktapUniversal},
		{"Sechno API", FetchSechnoUniversal},
		{"Terabox Mayumi", FetchTeraboxDirectUniversal},
		{"Iteraplay", FetchIteraMediaUniversal},
		{"FlowVideoPlayer", fetchFlowVideoPlayerUniversal},
		{"Netlify API", FetchTeraboxAPI2},
		{"Terabox Native", FetchTeraboxNativeUniversal},
	}

	var lastErr error
	for _, apiItem := range apis {
		cb := registry.Get(apiItem.name)

		// Cek state circuit breaker
		if cb.State() == gobreaker.StateOpen {
			logWarn("API Terabox di-skip (circuit breaker OPEN / cooldown)",
				zap.String("api", apiItem.name),
			)
			continue
		}

		logInfo("Mencoba API Terabox", zap.String("api", apiItem.name), zap.String("url", teraboxURL))

		// Eksekusi request di dalam circuit breaker
		data, err := cb.Execute(func() ([]TeraboxUniversalData, error) {
			res, err := apiItem.fn(teraboxURL)
			if err != nil {
				return nil, err
			}
			if len(res) == 0 {
				return nil, fmt.Errorf("respons kosong dari %s", apiItem.name)
			}
			return res, nil
		})

		if err == nil && len(data) > 0 {
			logInfo("API Terabox berhasil", zap.String("api", apiItem.name), zap.Int("total_files", len(data)))
			return data, nil
		}

		lastErr = err
		logWarn("API Terabox gagal", zap.String("api", apiItem.name), zap.Error(err))
	}

	logError("Semua API Terabox gagal", zap.String("url", teraboxURL), zap.Error(lastErr))
	return nil, fmt.Errorf("semua API Terabox gagal: %w", lastErr)
}

func fetchFlowVideoPlayerUniversal(teraboxURL string) ([]TeraboxUniversalData, error) {
	results, err := FetchMedia(teraboxURL)
	if err != nil {
		return nil, err
	}
	var universal []TeraboxUniversalData
	for _, res := range results {
		universal = append(universal, ResultToUniversal(res))
	}
	return universal, nil
}

func ResultToUniversal(r Result) TeraboxUniversalData {
	return TeraboxUniversalData{
		ID:          r.ID,
		FileName:    r.FileName,
		FileSize:    r.FileSize,
		Thumbnail:   r.CoverURL,
		StreamURL:   r.StreamURL,
		DownloadURL: r.FileURL,
	}
}

func ResetCircuitBreaker(apiName string) {
	registry.Reset(apiName)
}
