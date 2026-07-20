package panel

import (
	"context"
	"net"
	"time"
)

type NetworkManager interface {
	Discover(context.Context, string, string) (NetworkInfo, *net.IPNet, error)
	ListAddresses(context.Context, string) ([]net.IP, error)
	AddAddress(context.Context, string, *net.IPNet) error
	DeleteAddress(context.Context, string, *net.IPNet) error
	WaitReady(context.Context, string, net.IP, time.Duration) error
	CheckEgress(context.Context, net.IP, string, time.Duration) error
}
