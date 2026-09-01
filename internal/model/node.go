package model

import "time"

const (
	ProtocolOpenVPNUDP   = "openvpn_udp"
	ProtocolOpenVPNTCP   = "openvpn_tcp"
	ProtocolSoftEtherTLS = "softether_https"
	ProtocolSSTP         = "sstp"
	ProtocolL2TP         = "l2tp"
)

type Node struct {
	HostName        string    `json:"hostName"`
	IP              string    `json:"ip"`
	Port            int       `json:"port"`
	Score           int64     `json:"score"`
	PingMS          int64     `json:"pingMs"`
	SpeedBPS        int64     `json:"speedBps"`
	CountryLong     string    `json:"countryLong"`
	CountryShort    string    `json:"countryShort"`
	Sessions        int64     `json:"sessions"`
	UptimeMS        int64     `json:"uptimeMs"`
	TotalUsers      int64     `json:"totalUsers"`
	TotalTraffic    int64     `json:"totalTraffic"`
	LogType         string    `json:"logType"`
	Operator        string    `json:"operator"`
	Message         string    `json:"message"`
	OpenVPNConfig   string    `json:"-"`
	Protocols       []string  `json:"protocols"`
	MeasuredBPS     int64     `json:"measuredBps"`
	MeasuredAt      time.Time `json:"measuredAt,omitzero"`
	SpeedTestFailed bool      `json:"speedTestFailed"`
	LastFailure     time.Time `json:"lastFailure,omitzero"`
	FailureReason   string    `json:"failureReason,omitempty"`
	RefreshedAt     time.Time `json:"refreshedAt"`
}

type RuntimeStatus struct {
	State           string    `json:"state"`
	ConnectingIP    string    `json:"connectingIp,omitempty"`
	Selection       string    `json:"selection"`
	ActiveIP        string    `json:"activeIp,omitempty"`
	ActiveHostName  string    `json:"activeHostName,omitempty"`
	ActiveProtocol  string    `json:"activeProtocol,omitempty"`
	LastRefresh     time.Time `json:"lastRefresh,omitzero"`
	LastHealthCheck time.Time `json:"lastHealthCheck,omitzero"`
	LastError       string    `json:"lastError,omitempty"`
	RefreshRunning  bool      `json:"refreshRunning"`
}
