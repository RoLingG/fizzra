package store

import (
	"errors"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// SQLData 是基于 SQLite/GORM 的 Data 实现
type SQLData struct {
	db *DB
}

// NewData 返回默认的持久化实现
func NewData(db *DB) *SQLData { return &SQLData{db: db} }

// Migrate 创建或更新每一张表
func (s *SQLData) Migrate() error {
	return s.db.AutoMigrate(
		&model.Project{},
		&model.Analysis{},
		&model.Issue{},
		&model.Rule{},
		&model.QualityGate{},
	)
}

func (s *SQLData) Projects() ProjectRepo         { return &projectRepo{db: s.db} }
func (s *SQLData) Analyses() AnalysisRepo        { return &analysisRepo{db: s.db} }
func (s *SQLData) Issues() IssueRepo             { return &issueRepo{db: s.db} }
func (s *SQLData) Rules() RuleRepo               { return &ruleRepo{db: s.db} }
func (s *SQLData) QualityGates() QualityGateRepo { return &qualityGateRepo{db: s.db} }

// translateError 把 GORM 的哨兵错误转成 store 的哨兵错误，代码库其余部分因此
func translateError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique constraint") || strings.Contains(msg, "duplicate") {
		return ErrConflict
	}
	return err
}

// ---------------------------------------------------------------------------
// 项目
// ---------------------------------------------------------------------------

type projectRepo struct{ db *DB }

func (r *projectRepo) Create(p *model.Project) error {
	return translateError(r.db.Create(p).Error)
}

func (r *projectRepo) Update(p *model.Project) error {
	return translateError(r.db.Save(p).Error)
}

func (r *projectRepo) Delete(id string) error {
	return translateError(r.db.Delete(&model.Project{}, "id = ?", id).Error)
}

func (r *projectRepo) Get(id string) (*model.Project, error) {
	var p model.Project
	if err := r.db.First(&p, "id = ?", id).Error; err != nil {
		return nil, translateError(err)
	}
	return &p, nil
}

func (r *projectRepo) GetByName(name string) (*model.Project, error) {
	var p model.Project
	if err := r.db.First(&p, "name = ?", name).Error; err != nil {
		return nil, translateError(err)
	}
	return &p, nil
}

func (r *projectRepo) List() ([]model.Project, error) {
	var out []model.Project
	if err := r.db.Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, translateError(err)
	}
	return out, nil
}

func (r *projectRepo) SetLastAnalysis(projectID, analysisID string) error {
	return translateError(r.db.Model(&model.Project{}).
		Where("id = ?", projectID).
		Update("last_analysis_id", analysisID).Error)
}

// ---------------------------------------------------------------------------
// 分析
// ---------------------------------------------------------------------------

type analysisRepo struct{ db *DB }

func (r *analysisRepo) Create(a *model.Analysis) error {
	return translateError(r.db.Create(a).Error)
}

func (r *analysisRepo) Update(a *model.Analysis) error {
	return translateError(r.db.Save(a).Error)
}

func (r *analysisRepo) Get(id string) (*model.Analysis, error) {
	var a model.Analysis
	if err := r.db.First(&a, "id = ?", id).Error; err != nil {
		return nil, translateError(err)
	}
	return &a, nil
}

func (r *analysisRepo) ListByProject(projectID string, limit int) ([]model.Analysis, error) {
	q := r.db.Where("project_id = ?", projectID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []model.Analysis
	if err := q.Find(&out).Error; err != nil {
		return nil, translateError(err)
	}
	return out, nil
}

func (r *analysisRepo) ListRecent(limit int) ([]model.Analysis, error) {
	var out []model.Analysis
	if err := r.db.Order("created_at DESC").Limit(limit).Find(&out).Error; err != nil {
		return nil, translateError(err)
	}
	return out, nil
}

func (r *analysisRepo) LatestForProject(projectID string) (*model.Analysis, error) {
	var a model.Analysis
	err := r.db.Where("project_id = ? AND status = ?", projectID, model.StatusSuccess).
		Order("created_at DESC").First(&a).Error
	if err != nil {
		return nil, translateError(err)
	}
	return &a, nil
}

func (r *analysisRepo) Trend(projectID string, limit int) ([]model.Analysis, error) {
	var out []model.Analysis
	err := r.db.Where("project_id = ? AND status = ?", projectID, model.StatusSuccess).
		Order("created_at DESC").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, translateError(err)
	}
	// 最早的在前，图表可以直接绘制
	slices.Reverse(out)
	return out, nil
}

func (r *analysisRepo) DeleteByProject(projectID string) error {
	return translateError(r.db.Delete(&model.Analysis{}, "project_id = ?", projectID).Error)
}

func (r *analysisRepo) Stats(projectID string) (AnalysisStats, error) {
	var rows []model.Analysis
	q := r.db.Where("status = ?", model.StatusSuccess)
	if projectID != "" {
		q = q.Where("project_id = ?", projectID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return AnalysisStats{}, translateError(err)
	}

	stats := AnalysisStats{Runs: len(rows)}
	for _, a := range rows {
		if stats.Runs > 0 {
			if stats.BestScore == 0 || a.Score > stats.BestScore {
				stats.BestScore = a.Score
			}
			if stats.WorstScore == 0 || a.Score < stats.WorstScore {
				stats.WorstScore = a.Score
			}
		}
		stats.AvgScore += a.Score
		switch a.GateStatus {
		case model.GatePassed:
			stats.PassedRuns++
		case model.GateFailed:
			stats.FailedRuns++
		}
	}
	if stats.Runs > 0 {
		stats.AvgScore /= float64(stats.Runs)
	}
	return stats, nil
}

// ---------------------------------------------------------------------------
// 问题
// ---------------------------------------------------------------------------

type issueRepo struct{ db *DB }

// BatchCreate 分块插入问题，SQLite 的变量上限会让一条带数千行的巨型 INSERT
// 失败，所以做了分页
func (r *issueRepo) BatchCreate(issues []model.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	const chunk = 200
	return translateError(r.db.CreateInBatches(issues, chunk).Error)
}

func (r *issueRepo) Get(id string) (*model.Issue, error) {
	var i model.Issue
	if err := r.db.First(&i, "id = ?", id).Error; err != nil {
		return nil, translateError(err)
	}
	return &i, nil
}

func (r *issueRepo) Update(i *model.Issue) error {
	return translateError(r.db.Save(i).Error)
}

func (r *issueRepo) applyFilter(q *gorm.DB, analysisID string, f IssueFilter) *gorm.DB {
	q = q.Where("analysis = ?", analysisID)
	if len(f.Severities) > 0 {
		q = q.Where("severity IN ?", toAny(f.Severities))
	}
	if len(f.Analyzers) > 0 {
		q = q.Where("analyzer IN ?", f.Analyzers)
	}
	if len(f.Rules) > 0 {
		q = q.Where("rule IN ?", f.Rules)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("status IN ?", f.Statuses)
	}
	if f.File != "" {
		q = q.Where("file LIKE ?", "%"+f.File+"%")
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		q = q.Where("(message LIKE ? OR file LIKE ? OR rule LIKE ?)", like, like, like)
	}
	if f.NewOnly {
		q = q.Where("is_new = ?", true)
	}
	return q
}

func (r *issueRepo) ListByAnalysis(analysisID string, f IssueFilter) ([]model.Issue, error) {
	q := r.applyFilter(r.db.Model(&model.Issue{}), analysisID, f)

	order := "severity ASC, file ASC, line ASC"
	switch f.OrderBy {
	case "file":
		order = "file ASC, line ASC"
	case "analyzer":
		order = "analyzer ASC, severity ASC"
	case "recent":
		order = "created_at DESC"
	}
	q = q.Order(order)

	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	if f.Offset > 0 {
		q = q.Offset(f.Offset)
	}

	var out []model.Issue
	if err := q.Find(&out).Error; err != nil {
		return nil, translateError(err)
	}
	return out, nil
}

func (r *issueRepo) CountByAnalysis(analysisID string, f IssueFilter) (int64, error) {
	var n int64
	q := r.applyFilter(r.db.Model(&model.Issue{}), analysisID, f)
	if err := q.Count(&n).Error; err != nil {
		return 0, translateError(err)
	}
	return n, nil
}

func (r *issueRepo) CountBySeverity(analysisID string) (map[model.Severity]int, error) {
	type row struct {
		Severity model.Severity
		Count    int
	}
	var rows []row
	err := r.db.Model(&model.Issue{}).
		Select("severity, count(*) as count").
		Where("analysis = ?", analysisID).
		Group("severity").Scan(&rows).Error
	if err != nil {
		return nil, translateError(err)
	}
	out := make(map[model.Severity]int, len(rows))
	for _, r := range rows {
		out[r.Severity] = r.Count
	}
	return out, nil
}

func (r *issueRepo) CountByAnalyzer(analysisID string) (map[string]int, error) {
	type row struct {
		Analyzer string
		Count    int
	}
	var rows []row
	err := r.db.Model(&model.Issue{}).
		Select("analyzer, count(*) as count").
		Where("analysis = ?", analysisID).
		Group("analyzer").Scan(&rows).Error
	if err != nil {
		return nil, translateError(err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Analyzer] = r.Count
	}
	return out, nil
}

func (r *issueRepo) CountByRule(analysisID string, limit int) ([]RuleStat, error) {
	type row struct {
		Rule     string
		Analyzer string
		Severity string
		Count    int
	}
	var rows []row
	q := r.db.Model(&model.Issue{}).
		Select("rule, analyzer, severity, count(*) as count").
		Where("analysis = ?", analysisID).
		Group("rule, analyzer, severity").
		Order("count DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, translateError(err)
	}

	out := make([]RuleStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, RuleStat{Rule: r.Rule, Analyzer: r.Analyzer, Severity: r.Severity, Count: r.Count})
	}
	return out, nil
}

func (r *issueRepo) Fingerprints(analysisID string) (map[string]struct{}, error) {
	var fps []string
	err := r.db.Model(&model.Issue{}).
		Where("analysis = ?", analysisID).
		Pluck("fingerprint", &fps).Error
	if err != nil {
		return nil, translateError(err)
	}
	out := make(map[string]struct{}, len(fps))
	for _, f := range fps {
		out[f] = struct{}{}
	}
	return out, nil
}

// FingerprintsByProject 返回本项目近期成功分析中记录过的所有问题指纹
// 它回答的是「我们在记忆范围内见过这个发现吗」，而不是「它和上一次扫描不同吗」。
// 一个问题出现、被修好、之后又复现，会被报成不是新增，因为它在这个窗口内被见过。
// 窗口有界，上千次运行的项目上查询仍然便宜，比 limit 次分析更早的发现会重新算作新增
func (r *issueRepo) FingerprintsByProject(projectID string, limit int) (map[string]struct{}, error) {
	var ids []string
	err := r.db.Model(&model.Analysis{}).
		Where("project_id = ? AND status = ?", projectID, model.StatusSuccess).
		Order("created_at DESC").
		Limit(limit).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, translateError(err)
	}
	if len(ids) == 0 {
		return map[string]struct{}{}, nil
	}

	var fps []string
	if err := r.db.Model(&model.Issue{}).
		Where("analysis IN ?", ids).
		Distinct().
		Pluck("fingerprint", &fps).Error; err != nil {
		return nil, translateError(err)
	}
	out := make(map[string]struct{}, len(fps))
	for _, f := range fps {
		out[f] = struct{}{}
	}
	return out, nil
}

func (r *issueRepo) DeleteByProject(projectID string) error {
	return translateError(r.db.Delete(&model.Issue{}, "project = ?", projectID).Error)
}

func toAny[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
