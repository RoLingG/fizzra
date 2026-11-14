package store

import (
	"fmt"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// Data 是 service 层使用的持久化契约
//
// 每个方法返回的都是 store 的错误而不是 GORM 的错误，service 因此永远不导入 ORM。
// 正是这一点让 SQLite 实现可以被替换（比如换成 PostgreSQL）而不必碰业务逻辑
type Data interface {
	Migrate() error

	Projects() ProjectRepo
	Analyses() AnalysisRepo
	Issues() IssueRepo
	Rules() RuleRepo
	QualityGates() QualityGateRepo
}

// 各仓储接口

type ProjectRepo interface {
	Create(p *model.Project) error
	Update(p *model.Project) error
	Delete(id string) error
	Get(id string) (*model.Project, error)
	GetByName(name string) (*model.Project, error)
	List() ([]model.Project, error)
	SetLastAnalysis(projectID, analysisID string) error
}

type AnalysisRepo interface {
	Create(a *model.Analysis) error
	Update(a *model.Analysis) error
	Get(id string) (*model.Analysis, error)
	ListByProject(projectID string, limit int) ([]model.Analysis, error)
	ListRecent(limit int) ([]model.Analysis, error)
	LatestForProject(projectID string) (*model.Analysis, error)
	DeleteByProject(projectID string) error
	// Trend 返回项目的评分历史，最早的在前
	Trend(projectID string, limit int) ([]model.Analysis, error)
	Stats(projectID string) (AnalysisStats, error)
}

type IssueRepo interface {
	BatchCreate(issues []model.Issue) error
	Get(id string) (*model.Issue, error)
	Update(i *model.Issue) error
	ListByAnalysis(analysisID string, filter IssueFilter) ([]model.Issue, error)
	CountByAnalysis(analysisID string, filter IssueFilter) (int64, error)
	CountBySeverity(analysisID string) (map[model.Severity]int, error)
	CountByAnalyzer(analysisID string) (map[string]int, error)
	CountByRule(analysisID string, limit int) ([]RuleStat, error)
	// Fingerprints 返回某次分析记录下来的问题指纹
	Fingerprints(analysisID string) (map[string]struct{}, error)
	// FingerprintsByProject 返回近期多次分析的指纹，用于把问题归类为新增还是既有
	FingerprintsByProject(projectID string, limit int) (map[string]struct{}, error)
	DeleteByProject(projectID string) error
}

type RuleRepo interface {
	Create(r *model.Rule) error
	Update(r *model.Rule) error
	Delete(id string) error
	Get(id string) (*model.Rule, error)
	GetByName(name string) (*model.Rule, error)
	List() ([]model.Rule, error)
	ListEnabled() ([]model.Rule, error)
	UpsertByName(r *model.Rule) error
	Count() (int64, error)
}

type QualityGateRepo interface {
	Get(projectID string) (*model.QualityGate, error)
	Save(g *model.QualityGate) error
	Delete(projectID string) error
}

// IssueFilter 收窄问题查询
type IssueFilter struct {
	Severities []model.Severity
	Analyzers  []string
	Rules      []string
	Statuses   []string
	File       string
	Search     string
	NewOnly    bool
	Limit      int
	Offset     int
	OrderBy    string
}

// RuleStat 是一条按规则聚合的行
type RuleStat struct {
	Rule     string `json:"rule"`
	Analyzer string `json:"analyzer"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

// AnalysisStats 汇总项目的扫描历史
type AnalysisStats struct {
	Runs       int     `json:"runs"`
	AvgScore   float64 `json:"avgScore"`
	BestScore  float64 `json:"bestScore"`
	WorstScore float64 `json:"worstScore"`
	PassedRuns int     `json:"passedRuns"`
	FailedRuns int     `json:"failedRuns"`
}

// ErrNotFound 由仓储在记录不存在时返回
var ErrNotFound = fmt.Errorf("record not found")

// ErrConflict 在唯一约束将被违反时返回
var ErrConflict = fmt.Errorf("record already exists")
