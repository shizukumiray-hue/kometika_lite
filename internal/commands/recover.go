package commands

import (
	"go.uber.org/zap"
)

// safeGo menjalankan fn di goroutine terpisah dengan pemulihan panic.
// Panic di goroutine mana pun tanpa recover akan mematikan seluruh proses,
// jadi semua goroutine yang di-spawn manual wajib lewat sini (atau memakai
// defer recoverGoroutine langsung di badan goroutine).
func safeGo(name string, fn func()) {
	go func() {
		defer recoverGoroutine(name)
		fn()
	}()
}

// recoverGoroutine memulihkan panic di goroutine, mencatatnya, dan mencegah
// proses mati. Dipanggil via defer di awal goroutine.
func recoverGoroutine(name string) {
	if r := recover(); r != nil {
		zap.L().Error("Panic di goroutine "+name,
			zap.Any("recover", r),
			zap.Stack("stack"),
		)
	}
}
