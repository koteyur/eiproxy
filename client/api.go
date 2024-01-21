package client

import (
	"context"
	"eiproxy/common"
	"eiproxy/protocol"
	"fmt"
	"net/http"
	"net/url"
)

func (c *client) connect(ctx context.Context) (port int, token protocol.Token, err error) {
	u, err := url.Parse(c.cfg.ServerURL)
	if err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to parse url: %w", err)
	}

	u = u.JoinPath("api/connect")

	q := u.Query()
	q.Add("proto", ProtocolVer)
	q.Add("client", ClientVer)
	u.RawQuery = q.Encode()

	var connResp protocol.ConnectionResponse

	err = common.MakeApiRequestWithContext(
		ctx, http.MethodPost, u.String(), c.cfg.UserKey.String(), nil, &connResp)
	if err != nil {
		return 0, protocol.Token{}, err
	}

	if connResp.ErrorCode != nil {
		return 0, protocol.Token{}, fmt.Errorf("server returned error: %v", *connResp.ErrorCode)
	}
	if connResp.ErrorMessage != nil {
		return 0, protocol.Token{}, fmt.Errorf("server returned error: %v", *connResp.ErrorMessage)
	}
	if connResp.Port == nil || connResp.Token == nil {
		return 0, protocol.Token{}, fmt.Errorf("server returned invalid response: %v", connResp)
	}

	return *connResp.Port, *connResp.Token, nil
}

func (c *client) GetUser(ctx context.Context) (protocol.UserResponse, error) {
	var response protocol.UserResponse

	reqURL, err := url.JoinPath(c.cfg.ServerURL, "api/user")
	if err != nil {
		return response, fmt.Errorf("failed to build request url: %w", err)
	}

	err = common.MakeApiRequestWithContext(
		ctx, http.MethodGet, reqURL, c.cfg.UserKey.String(), nil, &response)
	return response, err
}
