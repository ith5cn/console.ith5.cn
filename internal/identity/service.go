package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Service 编排登录、设备授权流与凭据轮换。它不认识 HTTP。
type Service struct {
	store       Store
	enrollments EnrollmentStore // 可为 nil：不支持接入码
	signer      *Signer
	local       Provider
	now         func() time.Time
	baseURL     string
	verifyAt    string
}

// WithEnrollments 启用接入码支持。
func (s *Service) WithEnrollments(es EnrollmentStore) *Service {
	s.enrollments = es
	return s
}

// NewService 构造身份服务。baseURL 用于拼设备授权的验证链接。
func NewService(store Store, signer *Signer, baseURL string) *Service {
	return &Service{
		store:    store,
		signer:   signer,
		local:    NewLocalProvider(store),
		now:      time.Now,
		baseURL:  baseURL,
		verifyAt: baseURL + "/activate",
	}
}

// WithClock 替换时钟，供测试使用。
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	s.signer.WithClock(now)
	return s
}

// Signer 暴露给 HTTP 中间件做验签。
func (s *Service) Signer() *Signer { return s.signer }

// Store 暴露给需要实时查状态的中间件。
func (s *Service) Store() Store { return s.store }

// ---------------------------------------------------------------
// 浏览器登录
// ---------------------------------------------------------------

// LoginResult 是密码登录的结果：账号以及它能进入的组织。
//
// 登录本身不签发令牌——一个账号可能属于多个组织，令牌是组织内的。
// 调用方拿到列表后再调 IssueSession 选一个组织进入。
type LoginResult struct {
	Account     Account
	Memberships []Membership
}

// LoginWithPassword 校验密码并列出可进入的组织。
func (s *Service) LoginWithPassword(ctx context.Context, email, password string) (LoginResult, error) {
	id, err := s.local.VerifyPassword(ctx, email, password)
	if err != nil {
		return LoginResult{}, err
	}
	acct, _, err := s.store.FindLocalAccount(ctx, id.Email)
	if err != nil {
		return LoginResult{}, err
	}
	ms, err := s.store.ListMemberships(ctx, acct.ID)
	if err != nil {
		return LoginResult{}, err
	}
	active := ms[:0]
	for _, m := range ms {
		if !m.Suspended {
			active = append(active, m)
		}
	}
	return LoginResult{Account: acct, Memberships: active}, nil
}

// IssueSession 为某个成员记录签发会话令牌（浏览器用）。
//
// accountID 必须与成员记录的账号一致：登录接口只证明了「你是这个账号」，
// 不能凭此进入别人的组织成员身份。
func (s *Service) IssueSession(ctx context.Context, accountID, userID string) (Tokens, error) {
	m, err := s.store.GetMembership(ctx, userID)
	if err != nil {
		return Tokens{}, err
	}
	if m.AccountID != accountID {
		return Tokens{}, ErrNotFound
	}
	if m.Suspended {
		return Tokens{}, ErrSuspended
	}
	access, err := s.signer.Issue(m, "", TokenKindSession, s.now())
	if err != nil {
		return Tokens{}, fmt.Errorf("签发会话令牌: %w", err)
	}
	return Tokens{AccessToken: access, ExpiresIn: int(AccessTokenTTL.Seconds()), Membership: m}, nil
}

// ---------------------------------------------------------------
// 设备授权流（RFC 8628 的简化形态）
// ---------------------------------------------------------------

// DeviceStartResult 返回给 CLI：device_code 只出现这一次。
type DeviceStartResult struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	Interval        int
	ExpiresIn       int
}

// DeviceStart 创建设备码。CLI 是终端程序，不适合处理密码，故走设备码。
//
// enrollmentID 非空时，设备码记住这次是凭接入码发起的：审批页据此展示申请的项目，
// 轮询成功后客户端据此创建绑定。接入码在审批时才核销。
func (s *Service) DeviceStart(ctx context.Context, fingerprint, hostname, os, enrollmentID string) (DeviceStartResult, error) {
	if fingerprint == "" {
		return DeviceStartResult{}, errors.New("fingerprint 必填")
	}
	code, err := RandomToken()
	if err != nil {
		return DeviceStartResult{}, err
	}
	userCode, err := NewUserCode()
	if err != nil {
		return DeviceStartResult{}, err
	}
	dc := DeviceCode{
		CodeHash: HashToken(code), UserCode: userCode,
		Fingerprint: fingerprint, Hostname: hostname, OS: os, EnrollmentID: enrollmentID,
		Status: "pending", ExpiresAt: s.now().Add(DeviceCodeTTL),
	}
	if err := s.store.CreateDeviceCode(ctx, dc); err != nil {
		return DeviceStartResult{}, fmt.Errorf("保存设备码: %w", err)
	}
	return DeviceStartResult{
		DeviceCode: code, UserCode: userCode, VerificationURL: s.verifyAt,
		Interval: DevicePollInterval, ExpiresIn: int(DeviceCodeTTL.Seconds()),
	}, nil
}

// PeekDeviceCode 供审批页在批准前查看设备码：设备信息与所带的接入码。
func (s *Service) PeekDeviceCode(ctx context.Context, userCode string) (DeviceCode, *Enrollment, error) {
	dc, err := s.store.GetDeviceCodeByUserCode(ctx, NormalizeUserCode(userCode))
	if err != nil || dc.Status != "pending" || !dc.ExpiresAt.After(s.now()) {
		return DeviceCode{}, nil, ErrCodeExpired
	}
	if dc.EnrollmentID == "" || s.enrollments == nil {
		return dc, nil, nil
	}
	e, err := s.enrollments.GetEnrollment(ctx, "", dc.EnrollmentID)
	if err != nil {
		return dc, nil, ErrEnrollmentUnusable
	}
	return dc, &e, nil
}

// DeviceApprove 是浏览器端的批准动作：已登录的成员输入 CLI 显示的 user_code。
//
// 设备登记到这个成员名下，之后该设备的令牌都属于这个组织成员身份。
// 带接入码的设备码在这里核销接入码；核销失败（过期、用尽、撤销）则不批准。
// 接入码所限定项目的权限由调用方在批准前检查——它不授予任何成员身份。
func (s *Service) DeviceApprove(ctx context.Context, userID, userCode string) error {
	m, err := s.store.GetMembership(ctx, userID)
	if err != nil {
		return err
	}
	if m.Suspended {
		return ErrSuspended
	}
	code := NormalizeUserCode(userCode)
	dc, err := s.store.GetDeviceCodeByUserCode(ctx, code)
	if err != nil {
		return ErrCodeExpired
	}
	if dc.Status != "pending" || !dc.ExpiresAt.After(s.now()) {
		return ErrCodeExpired
	}
	if dc.EnrollmentID != "" {
		if s.enrollments == nil {
			return ErrEnrollmentUnusable
		}
		if err := s.enrollments.ConsumeEnrollment(ctx, dc.EnrollmentID, s.now()); err != nil {
			return err
		}
	}
	machineID, err := s.store.UpsertMachine(ctx, Machine{
		OrgID: m.OrgID, UserID: m.UserID,
		Fingerprint: dc.Fingerprint, Hostname: dc.Hostname, OS: dc.OS,
	})
	if err != nil {
		return fmt.Errorf("登记设备: %w", err)
	}
	if err := s.store.ApproveDeviceCode(ctx, code, m.UserID, machineID, s.now()); err != nil {
		return ErrCodeExpired
	}
	return nil
}

// DevicePoll 由 CLI 轮询。批准后单次消费并签发设备令牌与刷新凭据。
func (s *Service) DevicePoll(ctx context.Context, deviceCode string) (Tokens, error) {
	hash := HashToken(deviceCode)
	dc, err := s.store.ConsumeDeviceCode(ctx, hash, s.now())
	if errors.Is(err, ErrNotFound) {
		status, exp, serr := s.store.DeviceCodeStatus(ctx, hash)
		switch {
		case errors.Is(serr, ErrNotFound):
			return Tokens{}, ErrCodeExpired
		case serr != nil:
			return Tokens{}, serr
		case status == "consumed" || !exp.After(s.now()):
			return Tokens{}, ErrCodeExpired
		default:
			return Tokens{}, ErrAuthorizationPending
		}
	}
	if err != nil {
		return Tokens{}, err
	}
	toks, err := s.issueDeviceTokens(ctx, dc.UserID, dc.MachineID, uuid.NewString())
	if err != nil {
		return Tokens{}, err
	}
	if dc.EnrollmentID != "" && s.enrollments != nil {
		if e, err := s.enrollments.GetEnrollment(ctx, "", dc.EnrollmentID); err == nil {
			toks.EnrollmentProjectIDs = e.ProjectIDs
		}
	}
	return toks, nil
}

// ---------------------------------------------------------------
// 刷新凭据轮换
// ---------------------------------------------------------------

// Refresh 用旧刷新凭据换一对新令牌。
//
// 旧凭据立即撤销。若旧凭据已经被撤销过却再次出现，说明它泄露了：
// 整族全部撤销，返回 ErrRefreshReused，客户端只能重新走设备授权。
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	now := s.now()
	old, err := s.store.GetRefreshToken(ctx, HashToken(refreshToken))
	if errors.Is(err, ErrNotFound) {
		return Tokens{}, ErrInvalidGrant
	}
	if err != nil {
		return Tokens{}, err
	}
	if old.RevokedAt != nil {
		if rerr := s.store.RevokeRefreshFamily(ctx, old.FamilyID, now); rerr != nil {
			return Tokens{}, rerr
		}
		return Tokens{}, ErrRefreshReused
	}
	if !old.ExpiresAt.After(now) {
		return Tokens{}, ErrInvalidGrant
	}
	if err := s.store.RevokeRefreshToken(ctx, old.TokenHash, now); err != nil {
		return Tokens{}, err
	}
	return s.issueDeviceTokens(ctx, old.UserID, old.MachineID, old.FamilyID)
}

// Logout 撤销某设备的全部刷新凭据。
func (s *Service) Logout(ctx context.Context, userID, machineID string) error {
	return s.store.RevokeMachineTokens(ctx, userID, machineID, s.now())
}

// issueDeviceTokens 签发设备令牌，并在同一族里生成新的刷新凭据。
func (s *Service) issueDeviceTokens(ctx context.Context, userID, machineID, familyID string) (Tokens, error) {
	m, err := s.store.GetMembership(ctx, userID)
	if err != nil {
		return Tokens{}, err
	}
	if m.Suspended {
		return Tokens{}, ErrSuspended
	}
	mc, err := s.store.GetMachine(ctx, machineID)
	if err != nil {
		return Tokens{}, err
	}
	if mc.RevokedAt != nil {
		return Tokens{}, ErrMachineRevoked
	}
	now := s.now()
	access, err := s.signer.Issue(m, machineID, TokenKindDevice, now)
	if err != nil {
		return Tokens{}, fmt.Errorf("签发设备令牌: %w", err)
	}
	refresh, err := RandomToken()
	if err != nil {
		return Tokens{}, err
	}
	if err := s.store.CreateRefreshToken(ctx, RefreshToken{
		TokenHash: HashToken(refresh), FamilyID: familyID,
		UserID: userID, MachineID: machineID, ExpiresAt: now.Add(RefreshTokenTTL),
	}); err != nil {
		return Tokens{}, fmt.Errorf("保存刷新凭据: %w", err)
	}
	return Tokens{
		AccessToken: access, RefreshToken: refresh,
		ExpiresIn: int(AccessTokenTTL.Seconds()), Membership: m, MachineID: machineID,
	}, nil
}
