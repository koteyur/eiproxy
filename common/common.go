package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type HttpError int

func (e HttpError) Error() string {
	return http.StatusText(int(e))
}

func MakeApiRequest(method, url string, authKey string, params, response any) error {
	return MakeApiRequestWithContext(context.Background(), method, url, authKey, params, response)
}

func MakeApiRequestWithContext(
	ctx context.Context,
	method, url, authKey string,
	params, response any,
) error {
	var timeout = 5 * time.Second

	if deadline, ok := ctx.Deadline(); ok && !deadline.IsZero() {
		timeout = time.Until(deadline)
	}

	var reader io.Reader
	if params != nil {
		requestData, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reader = bytes.NewReader(requestData)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if authKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authKey))
	}
	req.Header.Set("Content-type", "application/json")

	hc := http.Client{
		Timeout: timeout,
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return HttpError(resp.StatusCode)
	}

	if response != nil {
		decoder := json.NewDecoder(resp.Body)
		if err := decoder.Decode(response); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}
