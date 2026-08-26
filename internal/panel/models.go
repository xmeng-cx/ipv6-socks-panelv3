package panel

import "time"

const stateVersion = 3

type Proxy struct {
	ID            string    `json:"id"`
	Port          int       `json:"port"`
	Protocol      string    `json:"protocol"`
	IPv6          string    `json:"ipv6"`
	Status        string    `json:"status"`
	LastError     string    `json:"lastError,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	LastRotatedAt time.Time `json:"lastRotatedAt,omitempty"`
	Owner         string    `json:"owner"`
	Username      string    `json:"username"`
	Password      string    `json:"password"`
}

type User struct {
	Username          string    `json:"username"`
	Role              string    `json:"role"`
	PasswordSalt      string    `json:"passwordSalt"`
	PasswordHash      string    `json:"passwordHash"`
	ProxyPassword     string    `json:"proxyPassword"`
	SubscriptionToken string    `json:"subscriptionToken"`
	CreatedAt         time.Time `json:"createdAt"`
}

type UserView struct {
	Username          string    `json:"username"`
	Role              string    `json:"role"`
	ProxyCount        int       `json:"proxyCount"`
	CreatedAt         time.Time `json:"createdAt"`
	SubscriptionToken string    `json:"-"`
}

type PendingOperation struct {
	ID        string    `json:"id"`
	ProxyID   string    `json:"proxyId,omitempty"`
	Kind      string    `json:"kind"`
	Candidate string    `json:"candidate,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type State struct {
	Version       int                         `json:"version"`
	Proxies       []Proxy                     `json:"proxies"`
	Pending       map[string]PendingOperation `json:"pending,omitempty"`
	IPv6Prefix    string                      `json:"ipv6Prefix,omitempty"`
	IPv6Interface string                      `json:"ipv6Interface,omitempty"`
	Users         []User                      `json:"users,omitempty"`
	SessionSecret string                      `json:"sessionSecret,omitempty"`
	DirectRules   []string                    `json:"directRules,omitempty"`
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
	Owner       string              `json:"-"`
}
