package api

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const systemInfoTimeout = 5 * time.Second

type SystemInfo struct {
	OS          string
	Platform    string
	Kernel      string
	Uptime      time.Duration
	CPUModel    string
	CPUPercent  float64
	CPUTemp     string
	RAMTotalGB  float64
	RAMUsedGB   float64
	RAMPercent  float64
	SwapTotalGB float64
	SwapUsedGB  float64
	SwapPercent float64
	LocalIP     string
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
		Goroutines: runtime.NumGoroutine(),
		MemAllocMB: float64(memStats.Alloc) / (1024 * 1024),
		MemSysMB:   float64(memStats.Sys) / (1024 * 1024),
		NumGC:      memStats.NumGC,
		LocalIP:    GetLocalIP(),
		CPUTemp:    getCPUTemp(),
	}

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
	if info.Uptime > 0 {
		sb.WriteString(fmt.Sprintf("└ <b>Uptime:</b> <code>%s</code>\n\n", info.Uptime.Round(time.Second).String()))
	} else {
		sb.WriteString("└ <b>Uptime:</b> <code>N/A</code>\n\n")
	}

	sb.WriteString("🎛 <b>Go Runtime Status</b>\n")
	sb.WriteString(fmt.Sprintf("├ <b>Goroutines:</b> <code>%d</code>\n", info.Goroutines))
	sb.WriteString(fmt.Sprintf("├ <b>Alloc:</b> <code>%.2f MB</code>\n", info.MemAllocMB))
	sb.WriteString(fmt.Sprintf("├ <b>Sys Mem:</b> <code>%.2f MB</code>\n", info.MemSysMB))
	sb.WriteString(fmt.Sprintf("└ <b>GC Runs:</b> <code>%d</code>\n\n", info.NumGC))

	if info.RAMTotalGB > 0 {
		sb.WriteString("🧠 <b>Host Memory Status</b>\n")
		sb.WriteString(fmt.Sprintf("├ <b>RAM:</b> <code>%.2f GB / %.2f GB (%.1f%%)</code>\n", info.RAMUsedGB, info.RAMTotalGB, info.RAMPercent))
		if info.SwapTotalGB > 0 {
			sb.WriteString(fmt.Sprintf("└ <b>Swap:</b> <code>%.2f GB / %.2f GB (%.1f%%)</code>\n\n", info.SwapUsedGB, info.SwapTotalGB, info.SwapPercent))
		} else {
			sb.WriteString("└ <b>Swap:</b> <code>None</code>\n\n")
		}
	}

	if info.CPUTemp != "" {
		sb.WriteString(fmt.Sprintf("🌡️ <b>CPU Temp:</b> <code>%s</code>\n\n", info.CPUTemp))
	}

	sb.WriteString("🌐 <b>Network</b>\n")
	sb.WriteString(fmt.Sprintf("└ <b>IP Local:</b> <code>%s</code>", info.LocalIP))

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
