package bridge

type CSIPacket struct {
	Seq     uint32
	Amp     []float64
	Phase   []float64
	NodeID  string
	TsMs    int64
	RSSI    float64
	HasRSSI bool
}

type LinkSample struct {
	NodeID  string
	SelfMAC string
	SrcMAC  string
	RSSI    float64
}

type Person struct {
	ID         int     `json:"id"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Pose       string  `json:"pose"`
	Motion     float64 `json:"motion"`
	Confidence float64 `json:"confidence,omitempty"`
	Profile    string  `json:"profile,omitempty"`
}

type Frame struct {
	People     []Person `json:"people"`
	Count      int      `json:"count"`
	X          float64  `json:"x"`
	Y          float64  `json:"y"`
	Presence   bool     `json:"presence"`
	CSIRate    float64  `json:"csi_rate"`
	UdpOK      bool     `json:"udp_ok"`
	UDPNodes   int      `json:"udp_nodes,omitempty"`
	UDPLive    []string `json:"udp_live,omitempty"`
	Seq        uint32   `json:"seq"`
	Timestamp  int64    `json:"ts"`
	Room       Room     `json:"room"`
	MLSource   string   `json:"ml_source,omitempty"`
	Calibrated bool     `json:"calibrated,omitempty"`
	Stereo     bool     `json:"stereo,omitempty"`
}
