package cluster

import (
	"net"
	"sort"
	"strconv"
	"time"
)

// Status is the JSON-safe runtime view of this node's cluster connection.
// It intentionally exposes only secret-free configuration values.
type Status struct {
	Enabled             bool                `json:"enabled"`
	Role                string              `json:"role"`
	IsLeader            bool                `json:"is_leader"`
	LeaderHealthy       bool                `json:"leader_healthy"`
	LeaderAddr          string              `json:"leader_addr,omitempty"`
	LocalAddr           string              `json:"local_addr,omitempty"`
	PeerCount           int                 `json:"peer_count"`
	OnlineNodes         int                 `json:"online_nodes"`
	ExpectedReplicas    int                 `json:"expected_replicas"`
	QuorumNodesRequired int                 `json:"quorum_nodes_required"`
	HasQuorum           bool                `json:"has_quorum"`
	Version             uint64              `json:"version"`
	Config              ClusterConfigStatus `json:"config"`
	Nodes               []NodeStatus        `json:"nodes"`
}

// ClusterConfigStatus is the secret-free subset of Config useful to operators.
type ClusterConfigStatus struct {
	DNSAddr           string `json:"dns_addr,omitempty"`
	BindAddr          string `json:"bind_addr,omitempty"`
	Port              int    `json:"port,omitempty"`
	Replicas          int    `json:"replicas,omitempty"`
	SecurityEnabled   bool   `json:"security_enabled"`
	LockKey           string `json:"lock_key"`
	Prefix            string `json:"prefix"`
	RefreshInterval   string `json:"refresh_interval,omitempty"`
	HeartbeatInterval string `json:"heartbeat_interval,omitempty"`
	HeartbeatTimeout  string `json:"heartbeat_timeout,omitempty"`
	SyncInterval      string `json:"sync_interval,omitempty"`
	ForwardTimeout    string `json:"forward_timeout,omitempty"`
}

// NodeStatus is one node in the local node's visible cluster graph.
type NodeStatus struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Address   string `json:"address,omitempty"`
	Role      string `json:"role"`
	Self      bool   `json:"self"`
	Leader    bool   `json:"leader"`
	Connected bool   `json:"connected"`
}

// Status returns a point-in-time snapshot of the cluster connection. In
// disabled mode it reports the deployment as a healthy single-node instance.
func (c *Cluster) Status() Status {
	config := c.statusConfig()
	if c == nil || !c.enabled || c.bw == nil {
		return Status{
			Enabled:             false,
			Role:                "standalone",
			IsLeader:            true,
			LeaderHealthy:       true,
			PeerCount:           0,
			OnlineNodes:         1,
			ExpectedReplicas:    config.Replicas,
			QuorumNodesRequired: 1,
			HasQuorum:           true,
			Config:              config,
			Nodes: []NodeStatus{{
				ID:        "self",
				Label:     "This node",
				Address:   configuredAddress(config.BindAddr, config.Port),
				Role:      "standalone",
				Self:      true,
				Leader:    true,
				Connected: true,
			}},
		}
	}

	s := c.bw.Status()
	peers := append([]*net.UDPAddr(nil), s.Peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].String() < peers[j].String() })

	localAddr := addrString(s.LocalAddr)
	leaderAddr := addrString(s.LeaderAddr)
	if s.IsLeader && leaderAddr == "" {
		leaderAddr = localAddr
	}

	role := "follower"
	if s.IsLeader {
		role = "leader"
		if !s.LeaderHealthy {
			role = "leader_unhealthy"
		}
	}

	selfRole := role
	if selfRole == "leader_unhealthy" {
		selfRole = "leader"
	}
	nodes := []NodeStatus{{
		ID:        "self",
		Label:     "This node",
		Address:   firstNonEmpty(localAddr, configuredAddress(config.BindAddr, config.Port)),
		Role:      selfRole,
		Self:      true,
		Leader:    s.IsLeader,
		Connected: true,
	}}
	for i, peer := range peers {
		addr := peer.String()
		peerRole := "peer"
		isLeader := addr != "" && addr == leaderAddr
		if isLeader {
			peerRole = "leader"
		}
		nodes = append(nodes, NodeStatus{
			ID:        "peer-" + strconv.Itoa(i+1),
			Label:     "Peer " + strconv.Itoa(i+1),
			Address:   addr,
			Role:      peerRole,
			Leader:    isLeader,
			Connected: true,
		})
	}

	onlineNodes := s.PeerCount + 1
	return Status{
		Enabled:             true,
		Role:                role,
		IsLeader:            s.IsLeader,
		LeaderHealthy:       s.LeaderHealthy,
		LeaderAddr:          leaderAddr,
		LocalAddr:           localAddr,
		PeerCount:           s.PeerCount,
		OnlineNodes:         onlineNodes,
		ExpectedReplicas:    config.Replicas,
		QuorumNodesRequired: quorumNodesRequired(config.Replicas),
		HasQuorum:           s.HasQuorum,
		Version:             s.Version,
		Config:              config,
		Nodes:               nodes,
	}
}

func (c *Cluster) statusConfig() ClusterConfigStatus {
	if c == nil {
		return defaultStatusConfig(Config{})
	}
	cfg := defaultStatusConfig(c.cfg)
	if c.enabled && c.alan != nil {
		ac := c.alan.Config()
		cfg.BindAddr = firstNonEmpty(ac.BindAddr, cfg.BindAddr)
		cfg.Port = ac.Port
		cfg.Replicas = ac.Replicas
		cfg.SecurityEnabled = ac.Security.Enabled
		cfg.RefreshInterval = durationString(ac.RefreshInterval)
		cfg.HeartbeatInterval = durationString(ac.HeartbeatInterval)
		cfg.HeartbeatTimeout = durationString(ac.HeartbeatTimeout)
	}
	return cfg
}

func defaultStatusConfig(cfg Config) ClusterConfigStatus {
	port := cfg.Port
	if port == 0 {
		port = 5000
	}
	bindAddr := cfg.BindAddr
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}
	syncInterval := cfg.SyncInterval
	if syncInterval == 0 {
		syncInterval = 5 * time.Minute
	}
	forwardTimeout := cfg.ForwardTimeout
	if forwardTimeout == 0 {
		forwardTimeout = 30 * time.Second
	}
	return ClusterConfigStatus{
		DNSAddr:           cfg.DNSAddr,
		BindAddr:          bindAddr,
		Port:              port,
		Replicas:          cfg.Replicas,
		SecurityEnabled:   cfg.SecurityKey != "",
		LockKey:           firstNonEmpty(cfg.LockKey, "pika-leader"),
		Prefix:            firstNonEmpty(cfg.Prefix, "pika"),
		RefreshInterval:   durationString(cfg.RefreshInterval),
		HeartbeatInterval: durationString(cfg.HeartbeatInterval),
		HeartbeatTimeout:  durationString(cfg.HeartbeatTimeout),
		SyncInterval:      durationString(syncInterval),
		ForwardTimeout:    durationString(forwardTimeout),
	}
}

func addrString(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func configuredAddress(bindAddr string, port int) string {
	if bindAddr == "" || port == 0 {
		return ""
	}
	return net.JoinHostPort(bindAddr, strconv.Itoa(port))
}

func quorumNodesRequired(replicas int) int {
	if replicas <= 0 {
		return 1
	}
	return replicas/2 + 1
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}
