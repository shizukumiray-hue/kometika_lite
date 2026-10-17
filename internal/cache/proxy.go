package cache

import (
	"sort"
	"sync"
	"time"
)

type Proxy struct {
	Addr    string
	Type    string
	Latency time.Duration
}

const ProxyCacheTTL = 3 * time.Hour

var (
	proxyMu      sync.Mutex
	proxyList    []Proxy
	proxyFetched time.Time
)

func SetProxyList(list []Proxy) {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	proxyList = append([]Proxy(nil), list...)
	sort.SliceStable(proxyList, func(i, j int) bool {
		li, lj := proxyList[i].Latency, proxyList[j].Latency
		if li == 0 {
			return false
		}
		if lj == 0 {
			return true
		}
		return li < lj
	})
	proxyFetched = time.Now()
}

func GetProxyList() ([]Proxy, time.Time) {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	return append([]Proxy(nil), proxyList...), proxyFetched
}

func IsProxyFresh(fetchedAt time.Time) bool {
	return !fetchedAt.IsZero() && time.Since(fetchedAt) < ProxyCacheTTL
}
