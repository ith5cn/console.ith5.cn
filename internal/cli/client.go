package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ith5/ith5/internal/api"
)

// ErrRevoked 表示账号已被撤权。
//
// CLI 收到它就执行本地清理——这是离职回收里**唯一挂在员工必经路径上**
// 的机制（技术方案 §5）。它能被绕过（不启动、离线），因此敏感能力必须
// 放在服务端 API 之后，本地那份只是空壳（PRD §9.3）。
var ErrRevoked = errors.New("账号已被撤权")

var ErrUnauthorized = errors.New("未授权")

type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(base, token string) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any, extra map[string]string) (int, http.Header, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/v1"+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var e api.ErrorResp
		_ = json.NewDecoder(resp.Body).Decode(&e)
		switch {
		case e.Error == "revoked":
			return resp.StatusCode, resp.Header, ErrRevoked
		case resp.StatusCode == http.StatusUnauthorized:
			return resp.StatusCode, resp.Header, fmt.Errorf("%w: %s", ErrUnauthorized, e.Message)
		default:
			msg := e.Message
			if msg == "" {
				msg = resp.Status
			}
			return resp.StatusCode, resp.Header, fmt.Errorf("%s (%s)", msg, e.Error)
		}
	}
	if out != nil && resp.StatusCode != http.StatusNotModified {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, resp.Header, fmt.Errorf("解析响应: %w", err)
		}
	}
	return resp.StatusCode, resp.Header, nil
}

// ---- 认证 ----

func (c *Client) DeviceStart(ctx context.Context, fp, host, os string) (api.DeviceStartResp, error) {
	var out api.DeviceStartResp
	_, _, err := c.do(ctx, http.MethodPost, "/auth/device/start",
		api.DeviceStartReq{Fingerprint: fp, Hostname: host, OS: os}, &out, nil)
	return out, err
}

// DevicePoll 轮询。pending 时返回 (zero, false, nil)，让调用方继续退避重试。
func (c *Client) DevicePoll(ctx context.Context, deviceCode string) (api.TokenResp, bool, error) {
	var out api.TokenResp
	status, _, err := c.do(ctx, http.MethodPost, "/auth/device/poll",
		api.DevicePollReq{DeviceCode: deviceCode}, &out, nil)
	if status == http.StatusPreconditionRequired {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return out, true, nil
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (api.TokenResp, error) {
	var out api.TokenResp
	_, _, err := c.do(ctx, http.MethodPost, "/auth/refresh",
		api.RefreshReq{RefreshToken: refreshToken}, &out, nil)
	return out, err
}

// ---- 分发 ----

// Manifest 拉取清单。带上 etag 时若无变化返回 (zero, false, nil)。
func (c *Client) Manifest(ctx context.Context, etag string) (api.ManifestResp, string, bool, error) {
	var out api.ManifestResp
	extra := map[string]string{}
	if etag != "" {
		extra["If-None-Match"] = etag
	}
	status, hdr, err := c.do(ctx, http.MethodGet, "/manifest", nil, &out, extra)
	if err != nil {
		return out, "", false, err
	}
	if status == http.StatusNotModified {
		return out, etag, false, nil
	}
	return out, hdr.Get("ETag"), true, nil
}

func (c *Client) BundleVersion(ctx context.Context, bundleID string, version int) (api.BundleVersionResp, error) {
	var out api.BundleVersionResp
	_, _, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/bundles/%s/versions/%d", bundleID, version), nil, &out, nil)
	return out, err
}

func (c *Client) ReportEvents(ctx context.Context, evs []api.DistributionEventJSON) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	var out api.AcceptedResp
	_, _, err := c.do(ctx, http.MethodPost, "/distribution-events",
		api.DistributionEventsReq{Events: evs}, &out, nil)
	return out.Accepted, err
}

// ReportExecutionEvents 上传 L0 执行事件。
func (c *Client) ReportExecutionEvents(ctx context.Context, evs []api.ExecutionEventJSON) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	var out api.AcceptedResp
	_, _, err := c.do(ctx, http.MethodPost, "/events",
		api.ExecutionEventsReq{Events: evs}, &out, nil)
	return out.Accepted, err
}
