package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// ChunkResponse adalah hasil satu request HTTP Range.
type ChunkResponse struct {
	Body         io.ReadCloser
	StatusCode   int
	ContentRange string // header Content-Range, mis. "bytes 0-1023/1048576"
}

// ChunkGetter membuka HTTP request untuk rentang byte [start,end].
// end == -1 artinya full GET (bytes=0-). Status 206 = server menghormati Range.
type ChunkGetter func(ctx context.Context, start, end int64) (*ChunkResponse, error)

// maxChunkAttempts adalah jumlah percobaan per chunk / per full GET.
const maxChunkAttempts = 3

// ParallelDownload mengunduh konten URL ke file f memakai threads goroutine
// paralel lewat HTTP Range. Memakai WriterAt (file) karena chunk datang dalam
// urutan tak menentu — pola yang sama dengan mode Parallel downloader gotd.
//
// total adalah perkiraan ukuran dari metadata; kalau > 0 dipakai untuk membagi
// chunk, tetapi ukuran asli dari header Content-Range lebih diutamakan.
// Kalau server tidak mendukung Range (probe bytes=0-0 tidak membalas 206),
// otomatis fallback ke satu GET sekuensial — stream yang tidak bisa di-range
// tetap bisa diunduh, hanya saja tanpa paralel.
func ParallelDownload(ctx context.Context, f *os.File, total int64, threads int, get ChunkGetter) error {
	probe, err := get(ctx, 0, 0)
	if err != nil {
		return fmt.Errorf("cek dukungan range: %w", err)
	}
	probe.Body.Close()

	switch probe.StatusCode {
	case http.StatusPartialContent:
		// Content-Range total adalah ukuran asli (metadata sering dibulatkan).
		if t := parseContentRangeTotal(probe.ContentRange); t > 0 {
			total = t
		}
		if err := parallelChunks(ctx, f, total, threads, get); err != nil {
			zap.L().Warn("Download paralel gagal, fallback sekuensial", zap.Error(err))
			if terr := f.Truncate(0); terr != nil {
				return terr
			}
			return sequentialDownload(ctx, f, get)
		}
		return nil

	case http.StatusOK, http.StatusRequestedRangeNotSatisfiable:
		// Server mengabaikan Range atau file kosong → unduh biasa.
		return sequentialDownload(ctx, f, get)

	default:
		return fmt.Errorf("probe download: status %d", probe.StatusCode)
	}
}

// goRecover memulihkan panic di goroutine yang di-spawn manual di package api.
// Panic di goroutine mana pun tanpa recover akan mematikan seluruh proses.
func goRecover(name string) {
	if r := recover(); r != nil {
		zap.L().Error("Panic di goroutine "+name,
			zap.Any("recover", r),
			zap.Stack("stack"),
		)
	}
}

// parallelChunks membagi file menjadi threads chunk dan mengunduhnya bersamaan.
func parallelChunks(ctx context.Context, f *os.File, total int64, threads int, get ChunkGetter) error {
	if total <= 0 || threads < 2 {
		return sequentialDownload(ctx, f, get)
	}
	if int64(threads) > total {
		threads = int(total)
	}

	chunkSize := (total + int64(threads) - 1) / int64(threads)

	// file dulu ditentukan ukurannya = total (sparse di Linux), supaya ukuran
	// akhir selalu benar walau bytes terakhir yang keburu tertulis.
	if err := f.Truncate(total); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := 0; i < threads; i++ {
		start := int64(i) * chunkSize
		if start >= total {
			break
		}
		end := start + chunkSize - 1
		if end >= total {
			end = total - 1
		}
		wg.Add(1)
		go func(idx, start, end int64) {
			defer wg.Done()
			// Panic di goroutine chunk tanpa recover akan mematikan seluruh
			// proses — catat sebagai error chunk supaya ParallelDownload bisa
			// fallback sekuensial.
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("panik di chunk %d: %v", idx, r)
						cancel()
					}
					mu.Unlock()
				}
			}()
			var lastErr error
			for attempt := 0; attempt < maxChunkAttempts; attempt++ {
				if ctx.Err() != nil {
					return
				}
				res, gerr := get(ctx, start, end)
				if gerr != nil {
					lastErr = gerr
					continue
				}
				if res.StatusCode != http.StatusPartialContent {
					res.Body.Close()
					lastErr = fmt.Errorf("status %d", res.StatusCode)
					continue
				}
				n, cerr := io.Copy(io.NewOffsetWriter(f, start), res.Body)
				res.Body.Close()
				if cerr != nil {
					lastErr = cerr
					continue
				}
				if n != end-start+1 {
					lastErr = fmt.Errorf("body pendek: %d byte, harap %d", n, end-start+1)
					continue
				}
				return
			}
			if lastErr == nil {
				return
			}
			mu.Lock()
			if firstErr == nil {
				firstErr = fmt.Errorf("chunk %d (%d-%d): %w", idx, start, end, lastErr)
				cancel()
			}
			mu.Unlock()
		}(int64(i), start, end)
	}

	wg.Wait()
	return firstErr
}

// sequentialDownload mengunduh seluruh file dengan satu GET (bytes=0-).
func sequentialDownload(ctx context.Context, f *os.File, get ChunkGetter) error {
	var lastErr error
	for attempt := 0; attempt < maxChunkAttempts; attempt++ {
		res, err := get(ctx, 0, -1)
		if err != nil {
			lastErr = err
			continue
		}
		if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
			res.Body.Close()
			lastErr = fmt.Errorf("status %d", res.StatusCode)
			continue
		}
		_, cerr := io.Copy(io.NewOffsetWriter(f, 0), res.Body)
		res.Body.Close()
		if cerr != nil {
			lastErr = cerr
			continue
		}
		return nil
	}
	return lastErr
}

// parseContentRangeTotal mengekstrak ukuran total dari header
// "bytes start-end/total".
func parseContentRangeTotal(cr string) int64 {
	const prefix = "bytes "
	if !strings.HasPrefix(cr, prefix) {
		return 0
	}
	rest := cr[len(prefix):]
	slash := strings.LastIndex(rest, "/")
	if slash <= 0 {
		return 0
	}
	t, err := strconv.ParseInt(rest[slash+1:], 10, 64)
	if err != nil || t <= 0 {
		return 0
	}
	return t
}
