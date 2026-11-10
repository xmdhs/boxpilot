package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// probeAPI only handles the gRPC-Web transport envelope for an Empty request.
// It does not encode/decode business protobufs or depend on the API client package.
// Readiness requires a successful authenticated RPC AND rejected missing/wrong credentials.
func probeAPI(ctx context.Context, endpoint, secret string) error {
	tr := directTransport()
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(token string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/daemon.StartedService/GetVersion", bytes.NewReader([]byte{0, 0, 0, 0, 0}))
		if err != nil {
			return -1, err
		}
		req.Header.Set("Content-Type", "application/grpc-web+proto")
		req.Header.Set("X-Grpc-Web", "1")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return -1, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return 16, nil
		}
		if resp.StatusCode != 200 {
			return -1, fmt.Errorf("API HTTP %d", resp.StatusCode)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/grpc-web") {
			return -1, errors.New("API is not gRPC-Web")
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
		if err != nil {
			return -1, err
		}
		if len(b) > 1<<20 {
			return -1, errors.New("API response too large")
		}
		status := -1
		data := false
		trailers := false
		if s := resp.Header.Get("Grpc-Status"); s != "" {
			status, err = strconv.Atoi(s)
			if err != nil {
				return -1, err
			}
		}
		for len(b) > 0 {
			if len(b) < 5 {
				return -1, errors.New("truncated gRPC-Web envelope")
			}
			size := int64(binary.BigEndian.Uint32(b[1:5]))
			flags := b[0]
			b = b[5:]
			if size > int64(len(b)) {
				return -1, errors.New("truncated gRPC-Web frame")
			}
			frame := b[:int(size)]
			b = b[int(size):]
			if trailers {
				return -1, errors.New("data after gRPC-Web trailers")
			}
			switch flags {
			case 0:
				data = true
			case 0x80:
				trailers = true
				for _, line := range strings.Split(string(frame), "\r\n") {
					key, value, ok := strings.Cut(line, ":")
					if ok && textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(key)) == "Grpc-Status" {
						status, err = strconv.Atoi(strings.TrimSpace(value))
						if err != nil {
							return -1, err
						}
					}
				}
			default:
				return -1, errors.New("unsupported gRPC-Web frame")
			}
		}
		if status < 0 || (status == 0 && !data) {
			return -1, errors.New("missing gRPC-Web status or response")
		}
		return status, nil
	}
	status, err := call(secret)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("authenticated API rejected request (gRPC %d)", status)
	}
	for _, token := range []string{"", secret + "-invalid"} {
		status, err = call(token)
		if err != nil {
			return err
		}
		if status != 16 && status != 7 {
			return errors.New("API authentication is not enforced")
		}
	}
	return nil
}
