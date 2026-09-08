package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/ith5/ith5/internal/auth"
	"github.com/ith5/ith5/internal/db"
)

// deviceStart 创建设备码。CLI 是终端程序，不适合处理密码，故走 device code。
func (s *Server) deviceStart(w http.ResponseWriter, r *http.Request) {
	var req DeviceStartReq
	if !s.decode(w, r, &req) {
		return
	}
	if req.Fingerprint == "" {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "fingerprint 必填")
		return
	}

	code, err := auth.RandomToken()
	if err != nil {
		s.internal(w, r, err, "生成设备码")
		return
	}
	userCode, err := auth.NewUserCode()
	if err != nil {
		s.internal(w, r, err, "生成用户码")
		return
	}
	expires := time.Now().Add(auth.DeviceCodeTTL)
	if err := s.db.CreateDeviceCode(r.Context(), auth.HashToken(code), userCode,
		req.Fingerprint, req.Hostname, req.OS, expires); err != nil {
		s.internal(w, r, err, "保存设备码")
		return
	}

	s.writeJSON(w, http.StatusOK, DeviceStartResp{
		DeviceCode:      code,
		UserCode:        userCode,
		VerificationURL: s.baseURL + "/activate",
		Interval:        5,
		ExpiresIn:       int(auth.DeviceCodeTTL.Seconds()),
	})
}

// devicePoll 轮询设备码。批准后单次消费，同一个码只可能成功一次。
func (s *Server) devicePoll(w http.ResponseWriter, r *http.Request) {
	var req DevicePollReq
	if !s.decode(w, r, &req) {
		return
	}
	hash := auth.HashToken(req.DeviceCode)

	userID, machineID, err := s.db.ConsumeDeviceCode(r.Context(), hash)
	if errors.Is(err, db.ErrNotFound) {
		// 还没批准，或已过期，或码不存在——区分开来给 CLI 正确的退避信号
		status, expired, sErr := s.db.DeviceCodeStatus(r.Context(), hash)
		switch {
		case errors.Is(sErr, db.ErrNotFound):
			s.fail(w, r, http.StatusGone, "expired", "设备码不存在或已失效")
		case sErr != nil:
			s.internal(w, r, sErr, "查询设备码状态")
		case expired || status == "consumed":
			s.fail(w, r, http.StatusGone, "expired", "设备码已失效，请重新登录")
		default:
			s.fail(w, r, http.StatusPreconditionRequired, "authorization_pending", "等待用户在浏览器中批准")
		}
		return
	}
	if err != nil {
		s.internal(w, r, err, "消费设备码")
		return
	}
	s.issueTokens(w, r, userID, machineID, true)
}

// deviceActivate 是 Web 端的批准动作：登录 + 输入 CLI 显示的 user_code。
func (s *Server) deviceActivate(w http.ResponseWriter, r *http.Request) {
	var req DeviceActivateReq
	if !s.decode(w, r, &req) {
		return
	}
	if !s.allowPassword(r, req.OrgSlug+"/"+req.Email) {
		w.Header().Set("Retry-After", "60")
		s.fail(w, r, http.StatusTooManyRequests, "rate_limited",
			"该账号的尝试过于频繁，请稍后再试")
		return
	}
	user, pwHash, err := s.db.GetUserByEmail(r.Context(), req.OrgSlug, req.Email)
	if err != nil || pwHash == "" {
		// 固定文案，不泄露账号是否存在
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "账号或密码不正确")
		return
	}
	ok, err := auth.VerifyPassword(req.Password, pwHash)
	if err != nil || !ok {
		s.fail(w, r, http.StatusUnauthorized, "unauthorized", "账号或密码不正确")
		return
	}
	if user.Suspended {
		s.fail(w, r, http.StatusForbidden, "suspended", "账号已停用")
		return
	}

	userCode := auth.NormalizeUserCode(req.UserCode)
	dc, err := s.db.GetDeviceCodeByUserCode(r.Context(), userCode)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", "验证码无效或已过期")
		return
	}
	machineID, err := s.db.UpsertMachine(r.Context(), user.ID, dc.Fingerprint, dc.Hostname, dc.OS)
	if err != nil {
		s.internal(w, r, err, "登记设备")
		return
	}
	if err := s.db.ApproveDeviceCode(r.Context(), userCode, user.ID, machineID); err != nil {
		s.fail(w, r, http.StatusNotFound, "not_found", "验证码无效或已过期")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshReq
	if !s.decode(w, r, &req) {
		return
	}
	userID, machineID, err := s.db.UseRefreshToken(r.Context(), auth.HashToken(req.RefreshToken))
	switch {
	case errors.Is(err, db.ErrSuspended):
		// 让 CLI 能识别「已被撤权」并触发本地清理（技术方案 §5 离职回收）
		s.fail(w, r, http.StatusUnauthorized, "revoked", "账号已停用")
		return
	case errors.Is(err, db.ErrNotFound):
		s.fail(w, r, http.StatusUnauthorized, "invalid_grant", "刷新令牌无效或已过期")
		return
	case err != nil:
		s.internal(w, r, err, "校验刷新令牌")
		return
	}
	s.issueTokens(w, r, userID, machineID, false)
}

// issueTokens 签发访问令牌；withRefresh 时一并签发刷新令牌。
func (s *Server) issueTokens(w http.ResponseWriter, r *http.Request, userID, machineID string, withRefresh bool) {
	user, err := s.db.GetUser(r.Context(), userID)
	if err != nil {
		s.internal(w, r, err, "读取用户")
		return
	}
	if user.Suspended {
		s.fail(w, r, http.StatusForbidden, "suspended", "账号已停用")
		return
	}
	access, err := s.signer.Issue(user.ID, user.OrgID, user.Role, machineID, auth.PurposeCLI, time.Now())
	if err != nil {
		s.internal(w, r, err, "签发访问令牌")
		return
	}
	resp := TokenResp{
		AccessToken: access,
		ExpiresIn:   int(auth.AccessTokenTTL.Seconds()),
		MachineID:   machineID,
		User:        UserInfo{ID: user.ID, Email: user.Email, Role: user.Role, OrgID: user.OrgID},
	}
	if withRefresh {
		refreshTok, err := auth.RandomToken()
		if err != nil {
			s.internal(w, r, err, "生成刷新令牌")
			return
		}
		if err := s.db.CreateRefreshToken(r.Context(), auth.HashToken(refreshTok),
			user.ID, machineID, time.Now().Add(auth.RefreshTokenTTL)); err != nil {
			s.internal(w, r, err, "保存刷新令牌")
			return
		}
		resp.RefreshToken = refreshTok
	}
	s.writeJSON(w, http.StatusOK, resp)
}
