package panel

import "time"

const stateVersion = 1

type Proxy struct {
	ID            string    `json:"id"`
	Port          int       `json:"port"`
	IPv6          string    `json:"ipv6"`
	Status        string    `json:"status"`
	LastError     string    `json:"lastError,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	LastRotatedAt time.Time `json:"lastRotatedAt,omitempty"`
}

type PendingOperation struct {
	ID        string    `json:"id"`
	ProxyID   string    `json:"proxyId,omitempty"`
	Kind      string    `json:"kind"`
	Candidate string    `json:"candidate,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type State struct {
	Version int                         `json:"version"`
	Proxies []Proxy                     `json:"proxies"`
	Pending map[string]PendingOperation `json:"pending,omitempty"`
}

type NetworkInfo struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
	IPv4      string `json:"ipv4,omitempty"`
}

type JobItem struct {
	ProxyID string `json:"proxyId"`
	Status  string `json:"status"`
	OldIPv6 string `json:"oldIpv6,omitempty"`
	NewIPv6 string `json:"newIpv6,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Job struct {
	ID          string              `json:"id"`
	Status      string              `json:"status"`
	Total       int                 `json:"total"`
	Completed   int                 `json:"completed"`
	Succeeded   int                 `json:"succeeded"`
	Failed      int                 `json:"failed"`
	CreatedAt   time.Time           `json:"createdAt"`
	CompletedAt time.Time           `json:"completedAt,omitempty"`
	Items       map[string]*JobItem `json:"items"`
}
