package protocol

type ConnectionResponse struct {
	Token        *Token          `json:"token,omitempty"`
	Port         *int            `json:"port,omitempty"`
	ErrorCode    *ConnectionCode `json:"error_code,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
}

type ConnectionCode byte

const (
	ConnectionCodeOk ConnectionCode = iota
	ConnectionCodeAlreadyConnected
	ConnectionCodeServerFull
	ConnectionCodeInternalError
)

func (c ConnectionCode) String() string {
	switch c {
	case ConnectionCodeOk:
		return "ok"
	case ConnectionCodeAlreadyConnected:
		return "already connected"
	case ConnectionCodeServerFull:
		return "server full"
	case ConnectionCodeInternalError:
		return "internal error"
	default:
		return "unknown"
	}
}

func (c ConnectionCode) Error() string {
	return c.String()
}
