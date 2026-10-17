package cache

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

var (
	janitorOnce sync.Once
	janitorStop chan struct{}
)

// StartJanitor menjalankan pembersih TTL tiap 10 menit.
func StartJanitor() {
	janitorOnce.Do(func() {
		stop := make(chan struct{})
		janitorStop = stop
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					func() {
						defer func() {
							if r := recover(); r != nil {
								zap.L().Error("Janitor panic", zap.Any("recover", r))
							}
						}()
						pruneAudio()
						ClearOldChannels()
					}()
				case <-stop:
					return
				}
			}
		}()
	})
}

// StopJanitor menghentikan janitor (dipanggil saat shutdown).
func StopJanitor() {
	if janitorStop != nil {
		close(janitorStop)
	}
}

func pruneAudio() {
	now := time.Now()
	audioStore.Range(func(key, value any) bool {
		if info, ok := value.(AudioInfo); ok && now.After(info.ExpiresAt) {
			audioStore.Delete(key)
		}
		return true
	})
}
