// Package identitytest 提供内存版 identity.Store，供 identity 与 api 的单元测试共用。
package identitytest

import (
	"context"
	"sync"
	"time"

	"github.com/ith5/ith5/internal/identity"
)

// Store 是线程安全的内存实现。字段导出以便测试直接摆数据。
type Store struct {
	mu sync.Mutex

	Accounts    map[string]identity.Account // by id
	Passwords   map[string]string           // account id -> hash
	Memberships map[string]identity.Membership
	DeviceCodes map[string]identity.DeviceCode // by code hash
	Machines    map[string]identity.Machine
	Refresh     map[string]identity.RefreshToken
	Enrollments map[string]identity.Enrollment

	nextID int
}

// New 创建空 Store。
func New() *Store {
	return &Store{
		Accounts:    map[string]identity.Account{},
		Passwords:   map[string]string{},
		Memberships: map[string]identity.Membership{},
		DeviceCodes: map[string]identity.DeviceCode{},
		Machines:    map[string]identity.Machine{},
		Refresh:     map[string]identity.RefreshToken{},
	}
}

// AddLocalUser 便捷地加一个 local 账号及其在某组织的成员记录，返回 (accountID, userID)。
func (s *Store) AddLocalUser(email, pwHash, orgID, orgSlug string, role identity.Role) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	acctID := "acct-" + itoa(s.nextID)
	s.Accounts[acctID] = identity.Account{ID: acctID, Issuer: identity.IssuerLocal, Subject: email, Email: email, Name: email}
	s.Passwords[acctID] = pwHash
	s.nextID++
	userID := "user-" + itoa(s.nextID)
	s.Memberships[userID] = identity.Membership{
		UserID: userID, AccountID: acctID, OrgID: orgID, OrgSlug: orgSlug, OrgName: orgSlug,
		Email: email, Name: email, Role: role,
	}
	return acctID, userID
}

func (s *Store) FindLocalAccount(_ context.Context, email string) (identity.Account, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.Accounts {
		if a.Issuer == identity.IssuerLocal && a.Subject == email {
			return a, s.Passwords[a.ID], nil
		}
	}
	return identity.Account{}, "", identity.ErrNotFound
}

func (s *Store) ListMemberships(_ context.Context, accountID string) ([]identity.Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []identity.Membership
	for _, m := range s.Memberships {
		if m.AccountID == accountID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Store) GetMembership(_ context.Context, userID string) (identity.Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.Memberships[userID]
	if !ok {
		return m, identity.ErrNotFound
	}
	return m, nil
}

func (s *Store) CreateDeviceCode(_ context.Context, dc identity.DeviceCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DeviceCodes[dc.CodeHash] = dc
	return nil
}

func (s *Store) GetDeviceCodeByUserCode(_ context.Context, userCode string) (identity.DeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, dc := range s.DeviceCodes {
		if dc.UserCode == userCode {
			return dc, nil
		}
	}
	return identity.DeviceCode{}, identity.ErrNotFound
}

func (s *Store) ApproveDeviceCode(_ context.Context, userCode, userID, machineID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, dc := range s.DeviceCodes {
		if dc.UserCode == userCode && dc.Status == "pending" && dc.ExpiresAt.After(now) {
			dc.Status, dc.UserID, dc.MachineID = "approved", userID, machineID
			s.DeviceCodes[h] = dc
			return nil
		}
	}
	return identity.ErrNotFound
}

func (s *Store) ConsumeDeviceCode(_ context.Context, codeHash string, now time.Time) (identity.DeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dc, ok := s.DeviceCodes[codeHash]
	if !ok || dc.Status != "approved" || !dc.ExpiresAt.After(now) {
		return identity.DeviceCode{}, identity.ErrNotFound
	}
	dc.Status = "consumed"
	s.DeviceCodes[codeHash] = dc
	return dc, nil
}

// ---- EnrollmentStore ----

func (s *Store) CreateEnrollment(_ context.Context, e identity.Enrollment) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Enrollments == nil {
		s.Enrollments = map[string]identity.Enrollment{}
	}
	s.nextID++
	e.ID = "enroll-" + itoa(s.nextID)
	s.Enrollments[e.ID] = e
	return e.ID, nil
}

func (s *Store) GetEnrollmentByHash(_ context.Context, codeHash string) (identity.Enrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.Enrollments {
		if e.CodeHash == codeHash {
			return e, nil
		}
	}
	return identity.Enrollment{}, identity.ErrNotFound
}

func (s *Store) GetEnrollment(_ context.Context, orgID, id string) (identity.Enrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Enrollments[id]
	if !ok || (orgID != "" && e.OrgID != orgID) {
		return identity.Enrollment{}, identity.ErrNotFound
	}
	return e, nil
}

func (s *Store) ListEnrollments(_ context.Context, orgID string) ([]identity.Enrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []identity.Enrollment
	for _, e := range s.Enrollments {
		if e.OrgID == orgID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Store) RevokeEnrollment(_ context.Context, orgID, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Enrollments[id]
	if !ok || e.OrgID != orgID {
		return identity.ErrNotFound
	}
	e.RevokedAt = &now
	s.Enrollments[id] = e
	return nil
}

func (s *Store) ConsumeEnrollment(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Enrollments[id]
	if !ok || !e.Usable(now) {
		return identity.ErrEnrollmentUnusable
	}
	e.UsedCount++
	s.Enrollments[id] = e
	return nil
}

func (s *Store) DeviceCodeStatus(_ context.Context, codeHash string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dc, ok := s.DeviceCodes[codeHash]
	if !ok {
		return "", time.Time{}, identity.ErrNotFound
	}
	return dc.Status, dc.ExpiresAt, nil
}

func (s *Store) UpsertMachine(_ context.Context, m identity.Machine) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, existing := range s.Machines {
		if existing.UserID == m.UserID && existing.Fingerprint == m.Fingerprint {
			existing.Hostname, existing.OS, existing.RevokedAt = m.Hostname, m.OS, nil
			s.Machines[id] = existing
			return id, nil
		}
	}
	s.nextID++
	m.ID = "machine-" + itoa(s.nextID)
	s.Machines[m.ID] = m
	return m.ID, nil
}

func (s *Store) GetMachine(_ context.Context, id string) (identity.Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.Machines[id]
	if !ok {
		return m, identity.ErrNotFound
	}
	return m, nil
}

func (s *Store) TouchMachine(_ context.Context, _ string, _ time.Time) error { return nil }

func (s *Store) CreateRefreshToken(_ context.Context, t identity.RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Refresh[t.TokenHash] = t
	return nil
}

func (s *Store) GetRefreshToken(_ context.Context, tokenHash string) (identity.RefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.Refresh[tokenHash]
	if !ok {
		return t, identity.ErrNotFound
	}
	return t, nil
}

func (s *Store) RevokeRefreshToken(_ context.Context, tokenHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.Refresh[tokenHash]; ok && t.RevokedAt == nil {
		t.RevokedAt = &now
		s.Refresh[tokenHash] = t
	}
	return nil
}

func (s *Store) RevokeRefreshFamily(_ context.Context, familyID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, t := range s.Refresh {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			t.RevokedAt = &now
			s.Refresh[h] = t
		}
	}
	return nil
}

func (s *Store) RevokeMachineTokens(_ context.Context, userID, machineID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, t := range s.Refresh {
		if t.UserID == userID && t.MachineID == machineID && t.RevokedAt == nil {
			t.RevokedAt = &now
			s.Refresh[h] = t
		}
	}
	return nil
}

// ActiveRefreshCount 统计未撤销的刷新凭据数，供断言。
func (s *Store) ActiveRefreshCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.Refresh {
		if t.RevokedAt == nil {
			n++
		}
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
