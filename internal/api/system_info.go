package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const systemInfoTimeout = 5 * time.Second

type SystemInfo struct {
	OS          string
	Platform    string
	Kernel      string
	Uptime      time.Duration
	CPUModel    string
	CPUCores    int
	CPUTemp     string
	RAMTotalGB  float64
	RAMUsedGB   float64
	RAMPercent  float64
	SwapTotalGB float64
	SwapUsedGB  float64
	SwapPercent float64
	DiskTotalGB float64
	DiskUsedGB  float64
	DiskPercent float64
	LocalIP     string
	PublicIP    string
	Goroutines  int
	MemSysMB    float64
	MemAllocMB  float64
	NumGC       uint32
}

func GetSystemInfo() (string, error) {
	info, err := GetSystemInfoExtended()
	return FormatSystemInfoText(info), err
}

func GetSystemInfoExtended() (*SystemInfo, error) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	info := &SystemInfo{
		OS:         runtime.GOOS,
		Platform:   runtime.GOARCH,
		Kernel:     runtime.Version(),
		CPUCores:   runtime.NumCPU(),
		CPUModel:   getCPUModel(),
		Goroutines: runtime.NumGoroutine(),
		MemAllocMB: float64(memStats.Alloc) / (1024 * 1024),
		MemSysMB:   float64(memStats.Sys) / (1024 * 1024),
		NumGC:      memStats.NumGC,
		LocalIP:    GetLocalIP(),
		PublicIP:   GetPublicIP(),
		CPUTemp:    getCPUTemp(),
	}

	// Disk storage info (root partition /)
	getDiskSpace("/", info)

	// Try reading /proc/uptime if Linux
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			if upSecs, err := strconv.ParseFloat(fields[0], 64); err == nil {
				info.Uptime = time.Duration(upSecs) * time.Second
			}
		}
	}

	// Try reading /proc/meminfo if Linux
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var memTotalKB, memAvailableKB, swapTotalKB, swapFreeKB float64
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				val, _ := strconv.ParseFloat(parts[1], 64)
				switch parts[0] {
				case "MemTotal:":
					memTotalKB = val
				case "MemAvailable:":
					memAvailableKB = val
				case "SwapTotal:":
					swapTotalKB = val
				case "SwapFree:":
					swapFreeKB = val
				}
			}
		}
		if memTotalKB > 0 {
			info.RAMTotalGB = memTotalKB / (1024 * 1024)
			memUsedKB := memTotalKB - memAvailableKB
			info.RAMUsedGB = memUsedKB / (1024 * 1024)
			info.RAMPercent = (memUsedKB / memTotalKB) * 100
		}
		if swapTotalKB > 0 {
			info.SwapTotalGB = swapTotalKB / (1024 * 1024)
			swapUsedKB := swapTotalKB - swapFreeKB
			info.SwapUsedGB = swapUsedKB / (1024 * 1024)
			info.SwapPercent = (swapUsedKB / swapTotalKB) * 100
		}
	}

	return info, nil
}

func FormatUptime(d time.Duration) string {
	if d <= 0 {
		return "N/A"
	}
	totalSec := int64(d.Round(time.Second).Seconds())
	days := totalSec / 86400
	hours := (totalSec % 86400) / 3600
	mins := (totalSec % 3600) / 60
	secs := totalSec % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, mins, secs)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, mins, secs)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
	return fmt.Sprintf("%ds", secs)
}

func FormatSystemInfoText(info *SystemInfo) string {
	if info == nil {
		return "⚠️ Gagal mengambil informasi sistem."
	}
	var sb strings.Builder

	sb.WriteString("💻 <b>SERVER MONITORING (LITE)</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")

	sb.WriteString("⚙️ <b>System Info</b>\n")
	sb.WriteString(fmt.Sprintf("├ <b>OS:</b> <code>%s (%s)</code>\n", info.OS, info.Platform))
	sb.WriteString(fmt.Sprintf("├ <b>Go:</b> <code>%s</code>\n", info.Kernel))
	sb.WriteString(fmt.Sprintf("└ <b>Uptime:</b> <code>%s</code>\n\n", FormatUptime(info.Uptime)))

	sb.WriteString("🎛 <b>Hardware & Runtime</b>\n")
	if info.CPUModel != "" {
		sb.WriteString(fmt.Sprintf("├ <b>CPU:</b> <code>%s (%d Cores)</code>\n", info.CPUModel, info.CPUCores))
	} else if info.CPUCores > 0 {
		sb.WriteString(fmt.Sprintf("├ <b>CPU Cores:</b> <code>%d Cores</code>\n", info.CPUCores))
	}
	if info.CPUTemp != "" {
		sb.WriteString(fmt.Sprintf("├ <b>CPU Temp:</b> <code>%s</code>\n", info.CPUTemp))
	}
	sb.WriteString(fmt.Sprintf("├ <b>Goroutines:</b> <code>%d</code>\n", info.Goroutines))
	sb.WriteString(fmt.Sprintf("├ <b>Alloc:</b> <code>%.2f MB</code>\n", info.MemAllocMB))
	sb.WriteString(fmt.Sprintf("├ <b>Sys Mem:</b> <code>%.2f MB</code>\n", info.MemSysMB))
	sb.WriteString(fmt.Sprintf("└ <b>GC Runs:</b> <code>%d</code>\n\n", info.NumGC))

	if info.RAMTotalGB > 0 || info.DiskTotalGB > 0 {
		sb.WriteString("🧠 <b>Storage & Memory</b>\n")
		if info.RAMTotalGB > 0 {
			sb.WriteString(fmt.Sprintf("├ <b>RAM:</b> <code>%.2f GB / %.2f GB (%.1f%%)</code>\n", info.RAMUsedGB, info.RAMTotalGB, info.RAMPercent))
			if info.SwapTotalGB > 0 {
				sb.WriteString(fmt.Sprintf("├ <b>Swap:</b> <code>%.2f GB / %.2f GB (%.1f%%)</code>\n", info.SwapUsedGB, info.SwapTotalGB, info.SwapPercent))
			}
		}
		if info.DiskTotalGB > 0 {
			sb.WriteString(fmt.Sprintf("└ <b>Disk (/):</b> <code>%.2f GB / %.2f GB (%.1f%%)</code>\n\n", info.DiskUsedGB, info.DiskTotalGB, info.DiskPercent))
		} else {
			sb.WriteString("\n")
		}
	}

	sb.WriteString("🌐 <b>Network</b>\n")
	sb.WriteString(fmt.Sprintf("├ <b>IP Local:</b> <code>%s</code>\n", info.LocalIP))
	sb.WriteString(fmt.Sprintf("└ <b>IP Publik:</b> <code>%s</code>", info.PublicIP))

	return sb.String()
}

func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "Tidak diketahui"
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "Tidak diketahui"
}

func GetPublicIP() string {
	client := &http.Client{
		Timeout: 2500 * time.Millisecond,
	}

	endpoints := []string{
		"https://api.ipify.org",
		"https://icanhazip.com",
		"https://ifconfig.me/ip",
	}

	for _, url := range endpoints {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
		_ = resp.Body.Close()
		if err != nil {
			continue
		}
		ipStr := strings.TrimSpace(string(body))
		if ip := net.ParseIP(ipStr); ip != nil {
			return ipStr
		}
	}

	return "Tidak diketahui"
}

func getCPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func getDiskSpace(path string, info *SystemInfo) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		if err := syscall.Statfs(".", &stat); err != nil {
			return
		}
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize)
	if totalBytes == 0 {
		return
	}

	usedBytes := totalBytes - freeBytes
	const GB = 1024 * 1024 * 1024
	info.DiskTotalGB = float64(totalBytes) / GB
	info.DiskUsedGB = float64(usedBytes) / GB
	info.DiskPercent = (float64(usedBytes) / float64(totalBytes)) * 100
}

func getCPUTemp() string {
	zones, err := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	if err != nil || len(zones) == 0 {
		return ""
	}
	var temps []float64
	for _, zone := range zones {
		data, err := os.ReadFile(zone)
		if err != nil {
			continue
		}
		tempMilli, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			continue
		}
		tempC := float64(tempMilli) / 1000.0
		if tempC > 0 && tempC < 100 {
			temps = append(temps, tempC)
		}
	}
	if len(temps) == 0 {
		return ""
	}
	var sum float64
	for _, t := range temps {
		sum += t
	}
	avg := sum / float64(len(temps))
	return fmt.Sprintf("%.1f°C", avg)
}
