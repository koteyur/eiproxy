package protocol

import "time"

const Version = "1.0"

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
	ConnectionCodeVersionMismatch
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
	case ConnectionCodeVersionMismatch:
		return "version mismatch"
	default:
		return "unknown"
	}
}

func (c ConnectionCode) Error() string {
	return c.String()
}

type UserResponse struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Port         int       `json:"port"`
	CreationTime time.Time `json:"creation_time"`
	LastUsedTime time.Time `json:"last_used_time"`
}
