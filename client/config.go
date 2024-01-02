package client

import "eiproxy/protocol"

type Config struct {
	MasterAddr string
	ServerURL  string
	UserKey    protocol.UserKey
}
