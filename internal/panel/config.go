package panel

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	WebListen        string
	DataDir          string
	XrayBinary       string
	XrayAPI          string
	SocksListen      string
	SocksUsername    string
	SocksPassword    string
	SocksUDP         bool
	UDPAdvertiseIP   string
	AdvertiseHost    string
	InitialProxies   int
	BasePort         int
	MaxProxies       int
	IPv6Interface    string
	IPv6Prefix       string
	IPCheckURL       string
	IPCheckTimeout   time.Duration
	DADTimeout       time.Duration
	RotateAllWorkers int
}

func LoadConfig() (Config, error) {
	c := Config{
		WebListen:        env("WEB_LISTEN", "0.0.0.0:8080"),
		DataDir:          env("DATA_DIR", "/data"),
		XrayBinary:       env("XRAY_BINARY", "/usr/local/bin/xray"),
		XrayAPI:          env("XRAY_API", "127.0.0.1:10085"),
		SocksListen:      env("SOCKS_LISTEN", "0.0.0.0"),
		SocksUsername:    env("SOCKS_USERNAME", "userb"),
		SocksPassword:    env("SOCKS_PASSWORD", "passwordb"),
		SocksUDP:         envBool("SOCKS_UDP", true),
		UDPAdvertiseIP:   strings.TrimSpace(os.Getenv("SOCKS_UDP_ADVERTISE_IP")),
		AdvertiseHost:    strings.TrimSpace(os.Getenv("ADVERTISE_HOST")),
		InitialProxies:   envInt("INITIAL_PROXIES", 10),
		BasePort:         envInt("BASE_PORT", 20000),
		MaxProxies:       envInt("MAX_PROXIES", 100),
		IPv6Interface:    strings.TrimSpace(os.Getenv("IPV6_INTERFACE")),
		IPv6Prefix:       strings.TrimSpace(os.Getenv("IPV6_PREFIX")),
		IPCheckURL:       env("IP_CHECK_URL", "https://api64.ipify.org"),
		IPCheckTimeout:   envDuration("IP_CHECK_TIMEOUT", 10*time.Second),
		DADTimeout:       envDuration("DAD_TIMEOUT", 8*time.Second),
		RotateAllWorkers: envInt("ROTATE_ALL_WORKERS", 4),
	}
	if c.SocksUsername == "" || c.SocksPassword == "" {
		return c, fmt.Errorf("SOCKS_USERNAME and SOCKS_PASSWORD must not be empty")
	}
	if c.InitialProxies < 0 || c.InitialProxies > c.MaxProxies {
		return c, fmt.Errorf("INITIAL_PROXIES must be between 0 and MAX_PROXIES")
	}
	if c.MaxProxies < 1 || c.MaxProxies > 100 {
		return c, fmt.Errorf("MAX_PROXIES must be between 1 and 100")
	}
	if c.BasePort < 1 || c.BasePort > 65535 {
		return c, fmt.Errorf("BASE_PORT must be between 1 and 65535")
	}
	if c.BasePort+c.MaxProxies-1 > 65535 {
		return c, fmt.Errorf("BASE_PORT + MAX_PROXIES exceeds 65535")
	}
	if c.RotateAllWorkers < 1 || c.RotateAllWorkers > 16 {
		return c, fmt.Errorf("ROTATE_ALL_WORKERS must be between 1 and 16")
	}
	return c, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return d
}
