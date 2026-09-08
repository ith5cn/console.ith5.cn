package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ith5/ith5/internal/core"
	"github.com/ith5/ith5/internal/db"
)

// manifest 返回该 principal 有权拿到的 Bundle 元数据，不含正文。
//
// 支持 ETag/If-None-Match：绝大多数 sync 是无变化的，条件请求几乎
// 零成本地砍掉一次往返和一条日志噪音（技术方案 §13.1）。
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)

	projectIDs, err := s.db.ProjectIDs(r.Context(), p.UserID)
	if err != nil {
		s.internal(w, r, err, "读取项目归属")
		return
	}
	data, err := s.db.LoadAuthzData(r.Context(), p.OrgID)
	if err != nil {
		s.internal(w, r, err, "读取授权数据")
		return
	}

	grants := core.Resolve(core.Principal{
		UserID:     p.UserID,
		OrgID:      p.OrgID,
		Role:       p.Role,
		ProjectIDs: projectIDs,
	}, data.Bundles, data.Groups, data.Assignments, time.Now())

	bundles := make([]ManifestBundle, 0, len(grants))
	for _, g := range grants {
		mb := ManifestBundle{
			ID: g.Bundle.ID, Name: g.Bundle.Name, Kind: string(g.Bundle.Kind),
			Version: g.Bundle.Version, Checksum: g.Bundle.Checksum,
			Description: g.Bundle.Description,
		}
		for _, v := range g.Via {
			mb.Via = append(mb.Via, GrantSourceInfo{
				SubjectType: string(v.SubjectType), GroupName: v.GroupName,
			})
		}
		bundles = append(bundles, mb)
	}

	etag := manifestETag(bundles)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	s.writeJSON(w, http.StatusOK, ManifestResp{
		GeneratedAt: time.Now().UTC(),
		TTLSeconds:  manifestTTLSeconds,
		Bundles:     bundles,
	})
}

// manifestETag 只对影响客户端行为的字段取摘要。
// 刻意不含 generated_at —— 否则每次请求 ETag 都变，条件请求就废了。
func manifestETag(bs []ManifestBundle) string {
	h := sha256.New()
	for _, b := range bs {
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%s\x00", b.ID, b.Name, b.Version, b.Checksum)
	}
	return `"` + hex.EncodeToString(h.Sum(nil))[:32] + `"`
}

// bundleVersion 返回正文。
//
// 这里**重新做一次权限判定**，不能只信客户端拿着的旧 manifest
// （技术方案 §2 原则 1）：sync 进行到一半时授权可能刚好被撤销或到期。
//
// 也**不在此写 DistributionLog** —— 服务端无从知道客户端是否校验成功、
// 是否真的落地，那要由客户端回执上报（§9）。
func (s *Server) bundleVersion(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	bundleID := chi.URLParam(r, "bundleID")
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		s.fail(w, r, http.StatusBadRequest, "bad_request", "版本号非法")
		return
	}
	if _, err := uuid.Parse(bundleID); err != nil {
		s.fail(w, r, http.StatusForbidden, "forbidden", "无权访问该内容")
		return
	}

	allowed, err := s.isAllowed(r, p, bundleID)
	if err != nil {
		s.internal(w, r, err, "权限判定")
		return
	}
	if !allowed {
		// 固定文案：不通过错误信息泄露 bundle 是否存在（技术方案 §6.3）
		s.fail(w, r, http.StatusForbidden, "forbidden", "无权访问该内容")
		return
	}

	meta, files, err := s.db.GetBundleVersion(r.Context(), p.OrgID, bundleID, version)
	if errors.Is(err, db.ErrNotFound) {
		s.fail(w, r, http.StatusForbidden, "forbidden", "无权访问该内容")
		return
	}
	if err != nil {
		s.internal(w, r, err, "读取正文")
		return
	}

	out := make([]FileJSON, len(files))
	for i, f := range files {
		out[i] = FileJSON{Path: f.Path, Content: f.Content}
	}
	s.writeJSON(w, http.StatusOK, BundleVersionResp{
		ID: meta.ID, Name: meta.Name, Kind: string(meta.Kind),
		Version: meta.Version, Checksum: meta.Checksum, Files: out,
	})
}

func (s *Server) isAllowed(r *http.Request, p principal, bundleID string) (bool, error) {
	projectIDs, err := s.db.ProjectIDs(r.Context(), p.UserID)
	if err != nil {
		return false, err
	}
	data, err := s.db.LoadAuthzData(r.Context(), p.OrgID)
	if err != nil {
		return false, err
	}
	grants := core.Resolve(core.Principal{
		UserID: p.UserID, OrgID: p.OrgID, Role: p.Role, ProjectIDs: projectIDs,
	}, data.Bundles, data.Groups, data.Assignments, time.Now())
	for _, g := range grants {
		if g.Bundle.ID == bundleID {
			return true, nil
		}
	}
	return false, nil
}

var validActions = map[string]bool{
	"install": true, "update": true, "remove": true,
	"relabel": true, "conflict_skipped": true,
}

// distributionEvents 接收客户端回执。
//
// 身份字段（org/user/machine/ip）一律由服务端补齐，**不接受客户端提交**。
// event_id 唯一，重复回执静默忽略（技术方案 §9）。
func (s *Server) distributionEvents(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	if p.MachineID == "" {
		s.fail(w, r, http.StatusForbidden, "forbidden", "该令牌未绑定设备")
		return
	}
	var req DistributionEventsReq
	if !s.decodeLenient(w, r, &req) {
		return
	}
	if len(req.Events) == 0 {
		s.writeJSON(w, http.StatusOK, AcceptedResp{Accepted: 0})
		return
	}
	if len(req.Events) > maxEventsPerBatch {
		s.fail(w, r, http.StatusBadRequest, "too_many",
			fmt.Sprintf("单次最多 %d 条", maxEventsPerBatch))
		return
	}

	evs := make([]db.DistributionEvent, 0, len(req.Events))
	for _, e := range req.Events {
		if _, err := uuid.Parse(e.EventID); err != nil {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "event_id 必须是 UUID")
			return
		}
		if _, err := uuid.Parse(e.BundleID); err != nil {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "bundle_id 必须是 UUID")
			return
		}
		if !validActions[e.Action] {
			s.fail(w, r, http.StatusBadRequest, "bad_request", "action 取值非法: "+e.Action)
			return
		}
		var detail []byte
		if len(e.Detail) > 0 {
			b, err := json.Marshal(e.Detail)
			if err != nil {
				s.fail(w, r, http.StatusBadRequest, "bad_request", "detail 格式非法")
				return
			}
			detail = b
		}
		occurred := e.OccurredAt
		if occurred.IsZero() {
			occurred = time.Now()
		}
		evs = append(evs, db.DistributionEvent{
			EventID: e.EventID, BundleID: e.BundleID, Version: e.Version,
			Action: e.Action, Detail: detail, OccurredAt: occurred,
		})
	}

	n, err := s.db.InsertDistributionEvents(r.Context(), p.OrgID, p.UserID, p.MachineID, clientIP(r), evs)
	if err != nil {
		s.internal(w, r, err, "写入分发回执")
		return
	}
	s.writeJSON(w, http.StatusOK, AcceptedResp{Accepted: n})
}
