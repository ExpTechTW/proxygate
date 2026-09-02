package vpn

import (
	"context"
	"fmt"
	"log"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/bclswl0827/govpn/protocols/l2tp"
	"github.com/bclswl0827/govpn/protocols/openvpn"
	"github.com/bclswl0827/govpn/protocols/softether"
	"github.com/bclswl0827/govpn/protocols/sstp"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/model"
)

type Session interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Wait(context.Context) error
	Close() error
}

type packetSession interface {
	ListenPacket(string, string) (net.PacketConn, error)
}

type Driver interface {
	Protocol() string
	Start(context.Context, model.Node, config.Config, *log.Logger) (Session, error)
}

func DefaultDrivers() []Driver {
	return []Driver{openVPNDriver{protocol: model.ProtocolOpenVPNUDP}, openVPNDriver{protocol: model.ProtocolOpenVPNTCP}, softEtherDriver{}, sstpDriver{}, l2tpDriver{}}
}

type openVPNDriver struct{ protocol string }

func (d openVPNDriver) Protocol() string { return d.protocol }

func (d openVPNDriver) Start(ctx context.Context, node model.Node, settings config.Config, logger *log.Logger) (Session, error) {
	if !slices.Contains(node.Protocols, d.protocol) {
		return nil, fmt.Errorf("node does not advertise %s", d.protocol)
	}
	parsed, err := openvpn.ParseConfig([]byte(node.OpenVPNConfig))
	if err != nil {
		return nil, fmt.Errorf("parse OpenVPN config: %w", err)
	}
	wantUDP := d.protocol == model.ProtocolOpenVPNUDP
	if wantUDP != strings.HasPrefix(parsed.Protocol, "udp") {
		return nil, fmt.Errorf("OpenVPN config transport does not match %s", d.protocol)
	}
	protocol := "tcp4"
	if wantUDP {
		protocol = "udp4"
	}
	parsed.Remote = node.IP
	parsed.Protocol = protocol
	for index := range parsed.Remotes {
		parsed.Remotes[index].Host = node.IP
		parsed.Remotes[index].Protocol = protocol
	}
	parsed.Username = settings.VPNGateUsername
	parsed.Password = settings.VPNGatePassword
	parsed.Logger = logger
	return openvpn.NewClient(*parsed).Start(ctx)
}

type softEtherDriver struct{}

func (softEtherDriver) Protocol() string { return model.ProtocolSoftEtherTLS }

func (softEtherDriver) Start(ctx context.Context, node model.Node, settings config.Config, logger *log.Logger) (Session, error) {
	port := node.Port
	if port == 0 {
		port = 443
	}
	return softether.NewClient(softether.Config{
		Server: node.IP, Port: port, Hub: "VPNGATE", Username: "vpn",
		AuthType: softether.AuthAnonymous, SkipVerify: true, OpenSSLCompat: true,
		ConnectTimeout: 5 * time.Second, DHCPTimeout: 15 * time.Second, Logger: logger,
	}).Start(ctx)
}

type sstpDriver struct{}

func (sstpDriver) Protocol() string { return model.ProtocolSSTP }

func (sstpDriver) Start(ctx context.Context, node model.Node, settings config.Config, logger *log.Logger) (Session, error) {
	port := node.Port
	if port == 0 {
		port = 443
	}
	return sstp.NewClient(sstp.Config{
		Server: node.IP, Port: port, Username: settings.VPNGateUsername,
		Password: settings.VPNGatePassword, SkipVerify: true, PrefixLength: 24, Logger: logger,
	}).Start(ctx)
}

type l2tpDriver struct{}

func (l2tpDriver) Protocol() string { return model.ProtocolL2TP }

func (l2tpDriver) Start(ctx context.Context, node model.Node, settings config.Config, logger *log.Logger) (Session, error) {
	// node.Port comes from the OpenVPN profile and is unrelated to IKE.
	// Leave IKEPort unset so the L2TP client uses UDP/500 and NAT-T UDP/4500.
	return l2tp.NewClient(l2tp.Config{
		Server: node.IP, PSK: settings.VPNGatePreSharedKey,
		Username: settings.VPNGateUsername, Password: settings.VPNGatePassword, Logger: logger,
	}).Start(ctx)
}
