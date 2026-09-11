package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ith5/ith5/internal/knowledge"
	"github.com/ith5/ith5/internal/organizations"
	"github.com/ith5/ith5/internal/resources"
)

// KnowledgeStore 实现 knowledge.Store：只读，写走变更集。
type KnowledgeStore struct{ db *DB }

// Knowledge 返回知识库仓储。
func (d *DB) Knowledge() *KnowledgeStore { return &KnowledgeStore{db: d} }

var _ knowledge.Store = (*KnowledgeStore)(nil)

// learningSelect 取当前版本、正文（用于标题 / 作者 / 摘要 / 检索）、投票合计。
// 正文来自 blobs，按 sha 取；learning 只有一个文件，files->0 即入口。
const learningSelect = `
	WITH cur AS (
		SELECT DISTINCT ON (bundle_id) bundle_id, id, version, files, deleted, published_at
		FROM bundle_versions ORDER BY bundle_id, version DESC
	), body AS (
		SELECT b.id AS bundle_id, convert_from(bl.bytes, 'UTF8') AS text
		FROM bundles b JOIN cur ON cur.bundle_id = b.id
		LEFT JOIN blobs bl ON bl.org_id = b.org_id AND bl.sha256 = cur.files->0->>'sha256'
		WHERE b.kind = 'learning'
	), votes AS (
		SELECT bundle_id, sum(recalled_count) AS recalled, sum(upvoted_count) AS upvoted, max(last_recalled_at) AS last_recall
		FROM knowledge_votes GROUP BY bundle_id
	)
	SELECT b.id::text, b.level::text, b.owner_id::text, coalesce(p.slug, 'common'), b.name, b.tags,
	       cur.version, cur.id::text, cur.deleted, cur.published_at,
	       coalesce(votes.recalled, 0), coalesce(votes.upvoted, 0), votes.last_recall,
	       coalesce(body.text, ''),
	       coalesce((SELECT u.email FROM users u WHERE u.id = (SELECT v.published_by FROM bundle_versions v WHERE v.bundle_id = b.id AND NOT v.deleted ORDER BY v.version LIMIT 1)), ''),
	       coalesce((SELECT v.published_by::text FROM bundle_versions v WHERE v.bundle_id = b.id AND NOT v.deleted ORDER BY v.version LIMIT 1), '')
	FROM bundles b
	JOIN cur ON cur.bundle_id = b.id
	LEFT JOIN body ON body.bundle_id = b.id
	LEFT JOIN votes ON votes.bundle_id = b.id
	LEFT JOIN projects p ON p.id = b.project_id
	WHERE b.org_id = $1 AND b.kind = 'learning' AND NOT b.archived`

func scanLearning(row pgx.Row, now time.Time) (knowledge.Learning, string, error) {
	var l knowledge.Learning
	var level, text, authorEmail string
	err := row.Scan(&l.ID, &level, &l.OwnerID, &l.Namespace, &l.Name, &l.Tags, &l.Version, &l.VersionID, &l.Archived,
		&l.PublishedAt, &l.Recalled, &l.Upvoted, &l.LastRecall, &text, &authorEmail, &l.AuthorID)
	if err != nil {
		return l, "", err
	}
	l.Level = resources.Level(level)
	fm, _ := resources.ParseFrontmatter(text)
	l.Title = fm.Name
	if t, ok := frontmatterValue(text, "title"); ok {
		l.Title = t
	}
	if l.Title == "" {
		l.Title = l.Name
	}
	if a, ok := frontmatterValue(text, "author"); ok {
		l.Author = a
	}
	if l.Author == "" {
		l.Author = authorEmail
	}
	if len(fm.Tags) > 0 && len(l.Tags) == 0 {
		l.Tags = fm.Tags
	}
	l.Excerpt = excerpt(text)
	l.Confidence = knowledge.Confidence(l.Recalled, l.Upvoted, l.LastRecall, now)
	return l, text, nil
}

func (s *KnowledgeStore) List(ctx context.Context, orgID string, f knowledge.ListFilter) ([]knowledge.Learning, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	status := f.Status
	if status == "" {
		status = "active"
	}
	rows, err := s.db.pool.Query(ctx, learningSelect+`
		  AND ($2 = '' OR b.project_id::text = $2 OR ($2 = 'shared' AND b.level = 'org'))
		  AND ($3 = 'all' OR ($3 = 'archived') = cur.deleted)
		  -- simple 解析器不切分中文，整段中文是一个词；因此再加子串匹配兜底
		  AND ($4 = '' OR to_tsvector('simple', coalesce(body.text, '') || ' ' || b.name) @@ plainto_tsquery('simple', $4)
		       OR b.name ILIKE '%' || $4 || '%' OR coalesce(body.text, '') ILIKE '%' || $4 || '%')
		ORDER BY cur.published_at DESC LIMIT $5`, orgID, f.ProjectID, status, f.Query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []knowledge.Learning{}
	for rows.Next() {
		l, _, err := scanLearning(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *KnowledgeStore) Get(ctx context.Context, orgID, bundleID string) (knowledge.Learning, error) {
	l, _, err := scanLearning(s.db.pool.QueryRow(ctx, learningSelect+` AND b.id = $2`, orgID, bundleID), time.Now())
	if isNoRows(err) {
		return l, knowledge.ErrNotFound
	}
	return l, err
}

func (s *KnowledgeStore) Content(ctx context.Context, orgID, bundleID string) (string, error) {
	_, text, err := scanLearning(s.db.pool.QueryRow(ctx, learningSelect+` AND b.id = $2`, orgID, bundleID), time.Now())
	if isNoRows(err) {
		return "", knowledge.ErrNotFound
	}
	return text, err
}

// Health 汇总知识库健康度：各 kind 数量、高频召回、沉默条目、归档 / 晋升候选、作者贡献、召回趋势。
func (s *KnowledgeStore) Health(ctx context.Context, orgID string, pol organizations.Policy) (knowledge.Health, error) {
	h := knowledge.Health{ByKind: map[string]int{}, ByAuthor: map[string]int{}, TopRecalled: []knowledge.Learning{},
		Silent: []knowledge.Learning{}, PruneCandidates: []knowledge.Learning{}, PromoteCandidates: []knowledge.Learning{}, RecallTrend: []knowledge.DayCount{}}
	rows, err := s.db.pool.Query(ctx, `
		WITH cur AS (SELECT DISTINCT ON (bundle_id) bundle_id, deleted FROM bundle_versions ORDER BY bundle_id, version DESC)
		SELECT b.kind::text, count(*) FROM bundles b JOIN cur ON cur.bundle_id = b.id
		WHERE b.org_id = $1 AND NOT b.archived AND NOT cur.deleted GROUP BY b.kind`, orgID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return h, err
		}
		h.ByKind[k] = n
	}
	rows.Close()

	all, err := s.List(ctx, orgID, knowledge.ListFilter{Status: "active", Limit: 200})
	if err != nil {
		return h, err
	}
	now := time.Now()
	for _, l := range all {
		h.ByAuthor[l.Author]++
		if l.Recalled == 0 {
			h.Silent = append(h.Silent, l)
		}
		if l.Confidence < pol.ConfidencePrune && now.Sub(l.PublishedAt) > 90*24*time.Hour {
			h.PruneCandidates = append(h.PruneCandidates, l)
		}
		if l.Confidence > pol.ConfidencePromote && l.Recalled >= 5 {
			h.PromoteCandidates = append(h.PromoteCandidates, l)
		}
	}
	sorted := append([]knowledge.Learning{}, all...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Recalled > sorted[i].Recalled {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for i, l := range sorted {
		if i >= 10 || l.Recalled == 0 {
			break
		}
		h.TopRecalled = append(h.TopRecalled, l)
	}

	trend, err := s.db.pool.Query(ctx, `
		SELECT to_char(received_at::date, 'YYYY-MM-DD'), count(*) FROM report_events
		WHERE type = 'vote_delta' AND received_at > now() - interval '30 days'
		  AND machine_id IN (SELECT id FROM machines WHERE org_id = $1)
		GROUP BY 1 ORDER BY 1`, orgID)
	if err != nil {
		return h, err
	}
	defer trend.Close()
	for trend.Next() {
		var d knowledge.DayCount
		if err := trend.Scan(&d.Day, &d.Count); err != nil {
			return h, err
		}
		h.RecallTrend = append(h.RecallTrend, d)
	}
	return h, trend.Err()
}

// frontmatterValue 读 frontmatter 里某个顶层标量。
func frontmatterValue(content, key string) (string, bool) {
	s := content
	if i := indexAfterFrontmatterStart(s); i >= 0 {
		s = s[i:]
	} else {
		return "", false
	}
	end := indexFrontmatterEnd(s)
	if end < 0 {
		return "", false
	}
	for _, line := range splitLines(s[:end]) {
		k, v, ok := cutColon(line)
		if ok && k == key {
			return trimQuotes(v), true
		}
	}
	return "", false
}

func indexAfterFrontmatterStart(s string) int {
	t := trimLeftSpace(s)
	if len(t) < 3 || t[:3] != "---" {
		return -1
	}
	off := len(s) - len(t) + 3
	for off < len(s) && s[off] != '\n' {
		off++
	}
	if off < len(s) {
		off++
	}
	return off
}

func indexFrontmatterEnd(s string) int {
	if len(s) >= 3 && s[:3] == "---" {
		return 0
	}
	for i := 0; i+4 <= len(s); i++ {
		if s[i] == '\n' && s[i+1:i+4] == "---" {
			return i
		}
	}
	return -1
}

func trimLeftSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	if len(s)-i >= 3 && s[i:i+3] == "\xEF\xBB\xBF" {
		i += 3
	}
	return s[i:]
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func cutColon(line string) (string, string, bool) {
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
		return "", "", false
	}
	for i := 0; i < len(line); i++ {
		if line[i] == ':' {
			return trimSpace(line[:i]), trimSpace(line[i+1:]), true
		}
	}
	return "", "", false
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

func trimQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// excerpt 取 frontmatter 之后的前 200 个字符作为摘要。
func excerpt(text string) string {
	body := text
	if i := indexAfterFrontmatterStart(text); i >= 0 {
		rest := text[i:]
		if end := indexFrontmatterEnd(rest); end >= 0 {
			body = rest[end:]
			for len(body) > 0 && (body[0] == '-' || body[0] == '\n' || body[0] == '\r') {
				body = body[1:]
			}
		}
	}
	runes := []rune(trimSpace(body))
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}
