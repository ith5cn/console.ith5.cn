package identity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Enrollment 是管理员签发的接入码（#341 旅程 J2）：一次性、限时、限定项目。
//
// 接入码本身不授予 bearer token。它只在设备授权流里起两个作用：
// 告诉服务端「这台设备该绑到哪些项目」，以及让审批页展示申请的范围。
type Enrollment struct {
	ID         string
	OrgID      string
	ProjectIDs []string
	CreatedBy  string
	CodeHash   string
	ExpiresAt  time.Time
	MaxUses    int
	UsedCount  int
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// Usable 判断此刻还能不能用。
func (e Enrollment) Usable(now time.Time) bool {
	return e.RevokedAt == nil && e.ExpiresAt.After(now) && e.UsedCount < e.MaxUses
}

// EnrollmentTTL 是接入码默认有效期。
const EnrollmentTTL = 7 * 24 * time.Hour

// ErrEnrollmentUnusable 表示接入码不存在、过期、用尽或已撤销。统一文案，不区分原因。
var ErrEnrollmentUnusable = errors.New("identity: 接入码无效")

// EnrollmentStore 是接入码的持久化能力。
type EnrollmentStore interface {
	CreateEnrollment(ctx context.Context, e Enrollment) (string, error)
	GetEnrollmentByHash(ctx context.Context, codeHash string) (Enrollment, error)
	GetEnrollment(ctx context.Context, orgID, id string) (Enrollment, error)
	ListEnrollments(ctx context.Context, orgID string) ([]Enrollment, error)
	RevokeEnrollment(ctx context.Context, orgID, id string, now time.Time) error
	// ConsumeEnrollment 以 compare-and-set 把 used_count 加一；不可用时返回 ErrEnrollmentUnusable。
	ConsumeEnrollment(ctx context.Context, id string, now time.Time) error
}

// EnrollmentService 签发与核销接入码。
type EnrollmentService struct {
	store EnrollmentStore
	now   func() time.Time
}

// NewEnrollmentService 构造服务。
func NewEnrollmentService(store EnrollmentStore) *EnrollmentService {
	return &EnrollmentService{store: store, now: time.Now}
}

// WithClock 替换时钟，供测试使用。
func (s *EnrollmentService) WithClock(now func() time.Time) *EnrollmentService {
	s.now = now
	return s
}

// Store 暴露仓储，供后台列表与撤销使用。
func (s *EnrollmentService) Store() EnrollmentStore { return s.store }

// Issue 生成接入码。明文只返回这一次。
func (s *EnrollmentService) Issue(ctx context.Context, orgID, createdBy string, projectIDs []string, ttl time.Duration, maxUses int) (code string, e Enrollment, err error) {
	if len(projectIDs) == 0 {
		return "", e, errors.New("接入码至少限定一个项目")
	}
	if ttl <= 0 || ttl > 30*24*time.Hour {
		ttl = EnrollmentTTL
	}
	if maxUses <= 0 {
		maxUses = 1
	}
	if maxUses > 1000 {
		return "", e, errors.New("max_uses 最多 1000")
	}
	code, err = RandomToken()
	if err != nil {
		return "", e, err
	}
	e = Enrollment{
		OrgID: orgID, ProjectIDs: projectIDs, CreatedBy: createdBy,
		CodeHash: HashToken(code), ExpiresAt: s.now().Add(ttl), MaxUses: maxUses,
	}
	e.ID, err = s.store.CreateEnrollment(ctx, e)
	if err != nil {
		return "", e, fmt.Errorf("保存接入码: %w", err)
	}
	return code, e, nil
}

// Resolve 按明文查接入码并确认可用。不消耗次数；消耗在设备被批准时发生。
func (s *EnrollmentService) Resolve(ctx context.Context, code string) (Enrollment, error) {
	e, err := s.store.GetEnrollmentByHash(ctx, HashToken(code))
	if err != nil {
		return Enrollment{}, ErrEnrollmentUnusable
	}
	if !e.Usable(s.now()) {
		return Enrollment{}, ErrEnrollmentUnusable
	}
	return e, nil
}

// Consume 核销一次。
func (s *EnrollmentService) Consume(ctx context.Context, id string) error {
	return s.store.ConsumeEnrollment(ctx, id, s.now())
}
