package bridge

import "sync"

type Room struct {
	Width  float64 `json:"width"`
	Depth  float64 `json:"depth"`
	ESPX   float64 `json:"esp_x"`
	ESPZ   float64 `json:"esp_z"`
	ESP2X  float64 `json:"esp2_x"`
	ESP2Z  float64 `json:"esp2_z"`
	TxX    float64 `json:"tx_x"`
	TxZ    float64 `json:"tx_z"`
	LinkM  float64 `json:"link_m,omitempty"`
	ESP1OK bool    `json:"esp1_ok"`
	ESP2OK bool    `json:"esp2_ok"`
	TxOK   bool    `json:"tx_ok"`

	MapGeometry string  `json:"map_geometry,omitempty"`
	MapScale    string  `json:"map_scale,omitempty"`
	ESPLinkM    float64 `json:"esp_link_m,omitempty"`
}

var (
	appRoomMu sync.RWMutex
	AppRoom   = Room{Width: 12, Depth: 12, TxOK: true}
)

func GetAppRoom() Room {
	appRoomMu.RLock()
	defer appRoomMu.RUnlock()
	return AppRoom
}

func SetAppRoom(r Room) {
	appRoomMu.Lock()
	AppRoom = r
	appRoomMu.Unlock()
}
