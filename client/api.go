package client

import (
	"context"
	"eiproxy/protocol"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

func (c *client) connect(ctx context.Context) (port int, token protocol.Token, err error) {
	reqURL, err := url.JoinPath(c.cfg.ServerURL, "api/connect")
	if err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to join url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("proto", ProtocolVer)
	q.Add("client", ClientVer)
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.cfg.UserKey))

	hc := http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, protocol.Token{}, fmt.Errorf("server returned: %s", http.StatusText(resp.StatusCode))
	}

	var connResp protocol.ConnectionResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&connResp); err != nil {
		return 0, protocol.Token{}, fmt.Errorf("failed to decode response: %w", err)
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
