package config

import "strings"

type PlatformDomain struct {
	Name    string   // Nama platform: "tiktok", "instagram", dll.
	Domains []string // Daftar domain/subdomain yang dikenali
}

// Daftar platform yang didukung di kometika-lite
var platforms = []PlatformDomain{
	{
		Name: "tiktok",
		Domains: []string{
			"tiktok.com",
			"vm.tiktok.com",
		},
	},
	{
		Name: "instagram",
		Domains: []string{
			"instagram.com",
			"instagr.am",
		},
	},
	{
		Name: "facebook",
		Domains: []string{
			"facebook.com",
			"fb.watch",
			"fb.gg",
			"fb.com",
		},
	},
	{
		Name: "terabox",
		Domains: []string{
			"terabox.app",
			"terabox.com",
			"terabox.link",
			"terabox.fun",
			"1024tera.com",
			"teraboxshare.com",
			"freeterabox.com", "teraboxapp.com", "1024terabox.com", "mirrobox.com",
			"nephobox.com", "4funbox.com", "dubox.com", "gibibox.com", "momerybox.com", "tibibox.com",
		},
	},
	{
		Name: "mediafire",
		Domains: []string{
			"mediafire.com",
			"mediafires.co",
			"mediafire.io",
			"mediafire.net",
			"mediafire.org",
		},
	},
	{
		Name: "twitter",
		Domains: []string{
			"twitter.com",
			"x.com",
			"t.co",
		},
	},
}

func checkDomainMatch(url, domain string) bool {
	lower := strings.ToLower(url)
	return strings.Contains(lower, "://"+domain) ||
		strings.Contains(lower, "."+domain) ||
		strings.Contains(lower, "/"+domain) ||
		strings.HasPrefix(lower, domain)
}

func IsSupportedURL(url string) bool {
	for _, p := range platforms {
		for _, d := range p.Domains {
			if checkDomainMatch(url, d) {
				return true
			}
		}
	}
	return false
}

func IsPlatformURL(url, platformName string) bool {
	for _, p := range platforms {
		if p.Name == platformName {
			for _, d := range p.Domains {
				if checkDomainMatch(url, d) {
					return true
				}
			}
		}
	}
	return false
}

func DetectPlatform(url string) string {
	for _, p := range platforms {
		for _, d := range p.Domains {
			if checkDomainMatch(url, d) {
				return p.Name
			}
		}
	}
	return ""
}

func IsTeraboxLink(url string) bool {
	return IsPlatformURL(url, "terabox")
}

func GetAllPlatforms() []string {
	names := make([]string, len(platforms))
	for i, p := range platforms {
		names[i] = p.Name
	}
	return names
}
