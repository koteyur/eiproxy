package client

import "eiproxy/protocol"

type Config struct {
	MasterAddr string
	ServerURL  string
	UserKey    protocol.UserKey
}

var DefaultConfig = Config{
	MasterAddr: "vps.gipat.ru:28004",
	ServerURL:  "http://localhost:8080",
}
