package domain

type Mode string

const (
	ModeHijack Mode = "hijack"
	ModeRoute  Mode = "route"
	ModeNormal Mode = "normal"
	ModeSpecificIP Mode = "specific_ip"
)

func (m Mode) Valid() bool {
	switch m {
	case ModeHijack, ModeRoute, ModeNormal, ModeSpecificIP:
		return true
	default:
		return false
	}
}
