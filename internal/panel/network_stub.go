//go:build !linux

package panel

import (
	"context"
	"fmt"
	"net"
	"time"
)

type unsupportedNetwork struct{}

func NewNetworkManager() NetworkManager { return &unsupportedNetwork{} }
func (n *unsupportedNetwork) Discover(context.Context, string, string) (NetworkInfo, *net.IPNet, error) {
	return NetworkInfo{}, nil, fmt.Errorf("IPv6 address management is supported only on Linux")
}
func (n *unsupportedNetwork) ListAddresses(context.Context, string) ([]net.IP, error) {
	return nil, fmt.Errorf("unsupported")
}
func (n *unsupportedNetwork) AddAddress(context.Context, string, *net.IPNet) error {
	return fmt.Errorf("unsupported")
}
func (n *unsupportedNetwork) DeleteAddress(context.Context, string, *net.IPNet) error {
	return fmt.Errorf("unsupported")
}
func (n *unsupportedNetwork) WaitReady(context.Context, string, net.IP, time.Duration) error {
	return fmt.Errorf("unsupported")
}
func (n *unsupportedNetwork) CheckEgress(context.Context, net.IP, string, time.Duration) error {
	return fmt.Errorf("unsupported")
}
