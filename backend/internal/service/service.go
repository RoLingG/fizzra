// Package service 承载业务逻辑，项目管理、运行分析、评分和报告
//
// api 包的 handler 只做协议适配并委托到这里，HTTP 层因此可以替换成命令行或 gRPC，
// 逻辑也能脱离服务器单独测试
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rolingg/fizzra/backend/internal/analyzer"
	"github.com/rolingg/fizzra/backend/internal/config"
	"github.com/rolingg/fizzra/backend/internal/model"
	"github.com/rolingg/fizzra/backend/internal/quality"
	"github.com/rolingg/fizzra/backend/internal/report"
	"github.com/rolingg/fizzra/backend/internal/store"
)

// ErrNotFound 在此转出，handler 因此不必导入 store 包就能把它映射成 404
var ErrNotFound = store.ErrNotFound

// ErrConflict 表示唯一性冲突（项目重名）
var ErrConflict = store.ErrConflict

// ErrInvalid 标记调用方输入有误，映射为 HTTP 400
var ErrInvalid = errors.New("invalid input")

// Service 是应用的门面
type Service struct {
	Data     store.Data
	Cfg      *config.Config
	Log      *slog.Logger
	Registry *analyzer.Registry
	Avail    *analyzer.Availability

	// Enqueuer 把分析排入队列异步执行
	Enqueuer Enqueuer
	// Engine 同步运行单次分析（命令行和 worker 使用，HTTP 层只调 Enqueuer）
	Engine *Engine
}

// Enqueuer 把一次分析提交到任务队列
type Enqueuer interface {
	Enqueue(analysisID string) error
	// Running 报告有多少任务在执行中，供系统接口使用
	Running() int
}

// ---------------------------------------------------------------------------
// 项目
// ---------------------------------------------------------------------------

// CreateProjectInput 是创建项目的入参
type CreateProjectInput struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	RepoURL     string `json:"repoUrl"`
	Branch      string `json:"branch"`
	LocalPath   string `json:"localPath"`
	// Language 是生态，决定运行哪些分析器。默认为 Go
	Language string `json:"language"`
	// Kind 是框架（"wails"），或空。与 Language 正交
	Kind      string   `json:"kind"`
	Color     string   `json:"color"`
	Analyzers []string `json:"analyzers"`
}

// normalizeLanguageKind 校验语言与框架的组合并补默认值
//
// 空语言按 Go 处理，与字段默认值对已有数据行的效果一致。空 kind 对任何语言都合法，
// 多数项目没有框架专项检查
func normalizeLanguageKind(language, kind string) (string, string, error) {
	if language == "" {
		language = model.LanguageGo
	}
	if !model.ValidLanguage(language) {
		return "", "", fmt.Errorf(
			"%w: language %q is not supported (valid: %v)",
			ErrInvalid, language, model.AllLanguages)
	}
	if !model.KindIsValidFor(language, kind) {
		return "", "", fmt.Errorf(
			"%w: kind %q is not valid for language %q", ErrInvalid, kind, language)
	}
	return language, kind, nil
}

// analyzersApplyToLanguage 报告给定分析器是否都适用于 lang
//
// 用于拒绝把项目固定到无法运行的分析器上，否则每次扫描都是静默的零发现
func (s *Service) analyzersApplyToLanguage(names []string, lang string) bool {
	if s.Registry == nil {
		return true
	}
	for _, n := range names {
		for _, a := range mustNew(s.Registry, n) {
			if !analyzer.AppliesToLanguage(a, lang) {
				return false
			}
		}
	}
	return true
}

// mustNew 按名实例化分析器，未知名字返回 nil
//
// 名字已由调用方校验，返回 nil 说明它被语言过滤掉了而不是拼错，拼错由调用方另行报告
func mustNew(r *analyzer.Registry, name string) []analyzer.Analyzer {
	out, err := r.New([]string{name})
	if err != nil {
		return nil
	}
	return out
}

// SetLanguage 更新项目的语言与框架
//
// 用于存储值与实际不符时，例如项目注册时还没有语言识别，或仓库后来才加上清单文件
// 不更新的话过期值会一直生效，之后每次扫描都跑错分析器，或者一个都不跑
func (s *Service) SetLanguage(ctx context.Context, id, language, kind string) (*model.Project, error) {
	language, kind, err := normalizeLanguageKind(language, kind)
	if err != nil {
		return nil, err
	}

	project, err := s.Data.Projects().Get(id)
	if err != nil {
		return nil, err
	}
	if project.Language == language && project.Kind == kind {
		return project, nil
	}

	// 固定到不适用于新语言的分析器会让扫描静默地什么都不做，所以清掉它，改用
	// 默认集合
	project.DefaultAnalyzers = nil
	project.Language = language
	project.Kind = kind
	project.UpdatedAt = time.Now()

	if err := s.Data.Projects().Update(project); err != nil {
		return nil, err
	}
	return project, nil
}

// CreateProject 注册一个项目
//
// 项目需要一个代码来源，已存在的本地路径或仓库地址，后者在首次扫描时克隆到工作区
func (s *Service) CreateProject(ctx context.Context, in CreateProjectInput) (*model.Project, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if in.LocalPath == "" && in.RepoURL == "" {
		return nil, fmt.Errorf("%w: either localPath or repoUrl is required", ErrInvalid)
	}

	if _, err := s.Data.Projects().GetByName(name); err == nil {
		return nil, fmt.Errorf("%w: a project named %q already exists", ErrConflict, name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	language, kind, err := normalizeLanguageKind(in.Language, in.Kind)
	if err != nil {
		return nil, err
	}

	// 提前校验分析器名：拼错应该在创建时就失败，而不是之后静默产生一次空扫描
	if len(in.Analyzers) > 0 {
		if _, err := s.Registry.New(in.Analyzers); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		// 固定到不适用于其语言的分析器，项目会什么都不扫却报出干净结果。在边界处
		// 拒绝，并指出哪一项不匹配
		if !s.analyzersApplyToLanguage(in.Analyzers, language) {
			return nil, fmt.Errorf(
				"%w: analyzers %v do not apply to language %q",
				ErrInvalid, in.Analyzers, language)
		}
	}

	localPath := in.LocalPath
	if localPath != "" {
		abs, err := filepath.Abs(localPath)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid localPath: %s", ErrInvalid, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("%w: localPath %s does not exist", ErrInvalid, abs)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: localPath %s is not a directory", ErrInvalid, abs)
		}
		localPath = abs
	}

	// 只有仓库项目才需要分支；本地路径留空，界面据此不显示分支
	branch := in.Branch
	if branch == "" && in.RepoURL != "" {
		branch = "main"
	}

	project := &model.Project{
		ID:               model.NewID("prj"),
		Name:             name,
		Description:      in.Description,
		RepoURL:          in.RepoURL,
		Branch:           branch,
		LocalPath:        localPath,
		Language:         language,
		Kind:             kind,
		Color:            defaultColor(in.Color),
		DefaultAnalyzers: in.Analyzers,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := s.Data.Projects().Create(project); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, fmt.Errorf("%w: a project named %q already exists", ErrConflict, name)
		}
		return nil, err
	}

	// 每个项目都配一个自己的门禁，设置页因此总是有具体的东西可编辑
	gate := &model.QualityGate{
		ID:            model.NewID("gat"),
		ProjectID:     project.ID,
		MinScore:      s.Cfg.Quality.MinScore,
		MaxCritical:   s.Cfg.Quality.MaxCritical,
		MaxHigh:       s.Cfg.Quality.MaxHigh,
		MaxComplexity: s.Cfg.Quality.MaxComplexity,
		MinCoverage:   s.Cfg.Quality.MinCoverage,
		Enabled:       true,
	}
	if err := s.Data.QualityGates().Save(gate); err != nil {
		s.Log.Warn("failed to create default quality gate", "project", project.ID, "err", err)
	}

	s.Log.Info("project created", "id", project.ID, "name", project.Name, "path", project.LocalPath)
	return project, nil
}

// UpdateProjectInput 携带项目可变的字段
type UpdateProjectInput struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	RepoURL     *string   `json:"repoUrl"`
	Branch      *string   `json:"branch"`
	LocalPath   *string   `json:"localPath"`
	Language    *string   `json:"language"`
	Kind        *string   `json:"kind"`
	Color       *string   `json:"color"`
	Analyzers   *[]string `json:"analyzers"`
}

// UpdateProject 应用一次部分更新
func (s *Service) UpdateProject(ctx context.Context, id string, in UpdateProjectInput) (*model.Project, error) {
	project, err := s.Data.Projects().Get(id)
	if err != nil {
		return nil, err
	}

	if in.Name != nil && *in.Name != project.Name {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalid)
		}
		if existing, err := s.Data.Projects().GetByName(name); err == nil && existing.ID != id {
			return nil, fmt.Errorf("%w: a project named %q already exists", ErrConflict, name)
		}
		project.Name = name
	}
	if in.Description != nil {
		project.Description = *in.Description
	}
	if in.RepoURL != nil {
		project.RepoURL = *in.RepoURL
		// 换成本地来源时分支一并清掉，否则会留下一个没有仓库的分支名
		if project.RepoURL == "" && in.Branch == nil {
			project.Branch = ""
		}
	}
	if in.Branch != nil {
		project.Branch = *in.Branch
	}
	if in.Color != nil {
		project.Color = defaultColor(*in.Color)
	}
	if in.Language != nil && *in.Language != project.Language {
		language := *in.Language
		if !model.ValidLanguage(language) {
			return nil, fmt.Errorf(
				"%w: language %q is not supported (valid: %v)",
				ErrInvalid, language, model.AllLanguages)
		}
		// kind 跟着语言走：wails 只对 Go 有意义，换语言后原来的 kind 多半已经不适用
		if in.Kind == nil && !model.KindIsValidFor(language, project.Kind) {
			project.Kind = ""
		}
		project.Language = language
	}
	if in.Kind != nil {
		if !model.KindIsValidFor(project.Language, *in.Kind) {
			return nil, fmt.Errorf("%w: kind %q is not valid for language %q", ErrInvalid, *in.Kind, project.Language)
		}
		project.Kind = *in.Kind
	}
	if in.LocalPath != nil {
		// 空串表示改用仓库来源，清掉本地路径
		if *in.LocalPath == "" {
			project.LocalPath = ""
		} else {
			abs, err := filepath.Abs(*in.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid localPath", ErrInvalid)
			}
			if _, err := os.Stat(abs); err != nil {
				return nil, fmt.Errorf("%w: localPath %s does not exist", ErrInvalid, abs)
			}
			project.LocalPath = abs
		}
	}
	if in.Analyzers != nil {
		if len(*in.Analyzers) > 0 {
			if _, err := s.Registry.New(*in.Analyzers); err != nil {
				return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
			}
			// 换语言后旧的分析器可能已经不适用，留着会产生一次静默的零问题扫描
			if !s.analyzersApplyToLanguage(*in.Analyzers, project.Language) {
				return nil, fmt.Errorf(
					"%w: some analyzers do not apply to language %q", ErrInvalid, project.Language)
			}
		}
		project.DefaultAnalyzers = *in.Analyzers
	}

	project.UpdatedAt = time.Now()
	if err := s.Data.Projects().Update(project); err != nil {
		return nil, err
	}
	return project, nil
}

// DeleteProject 删除项目以及由它派生的一切
func (s *Service) DeleteProject(ctx context.Context, id string) error {
	if _, err := s.Data.Projects().Get(id); err != nil {
		return err
	}

	// 表结构里没配级联，所以显式清理
	if err := s.deleteProjectRows(id); err != nil {
		return err
	}

	s.Log.Info("project deleted", "id", id)
	return nil
}

// deleteProjectRows 按正确顺序抹掉一个项目及其派生数据
//
// 顺序很重要，问题引用分析，分析引用项目，所以从叶子往根删。抽成函数是为了让
// 单个删除和批量删除共用同一实现，两份拷贝迟早会漂移，代价是删不干净或删错东西
func (s *Service) deleteProjectRows(id string) error {
	if err := s.Data.Issues().DeleteByProject(id); err != nil {
		return fmt.Errorf("delete issues: %w", err)
	}
	if err := s.Data.Analyses().DeleteByProject(id); err != nil {
		return fmt.Errorf("delete analyses: %w", err)
	}
	if err := s.Data.QualityGates().Delete(id); err != nil {
		return fmt.Errorf("delete quality gate: %w", err)
	}
	if err := s.Data.Projects().Delete(id); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// DeleteProjects 删除多个项目及其派生数据，返回实际删掉的 ID
//
// 逐个删而不是一条 SQL 批量删，因为清理跨四张表（问题、分析、门禁、项目），单条
// 语句表达不了这个顺序。数量由调用方限制，界面也不可能一次选中几万个
//
// 不存在的 ID 跳过而不是报错，并发下一个标签页可能刚把它删掉，用户「这些项目不要了」
// 的本意已经达成，报错只会让人误以为删除失败。数据库错误会中断并返回，此时前面已经
// 删掉的部分如实带回，返回值里的 ID 列表就是已删的那些
func (s *Service) DeleteProjects(ctx context.Context, ids []string) ([]string, error) {
	deleted := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		if _, err := s.Data.Projects().Get(id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return deleted, err
		}
		if err := s.deleteProjectRows(id); err != nil {
			return deleted, err
		}
		deleted = append(deleted, id)
	}

	s.Log.Info("projects deleted", "count", len(deleted))
	return deleted, nil
}

// GetProject 按 ID 返回项目
func (s *Service) GetProject(ctx context.Context, id string) (*model.Project, error) {
	return s.Data.Projects().Get(id)
}

// ListProjects 返回所有项目，并附上各自最近一次分析的摘要
func (s *Service) ListProjects(ctx context.Context) ([]ProjectSummary, error) {
	projects, err := s.Data.Projects().List()
	if err != nil {
		return nil, err
	}

	out := make([]ProjectSummary, 0, len(projects))
	for _, p := range projects {
		summary := ProjectSummary{Project: p}
		if latest, err := s.Data.Analyses().LatestForProject(p.ID); err == nil {
			summary.Latest = latest
		} else if !errors.Is(err, store.ErrNotFound) {
			s.Log.Warn("failed to load latest analysis", "project", p.ID, "err", err)
		}
		if stats, err := s.Data.Analyses().Stats(p.ID); err == nil {
			summary.Stats = stats
		}
		out = append(out, summary)
	}
	return out, nil
}

// ProjectSummary 是项目加上它最近的结果
type ProjectSummary struct {
	model.Project
	Latest *model.Analysis     `json:"latest,omitempty"`
	Stats  store.AnalysisStats `json:"stats"`
}

// ResolveProject 按 ID 或名字查找项目，命令行因此可以混用路径和名字
func (s *Service) ResolveProject(ctx context.Context, idOrName string) (*model.Project, error) {
	if p, err := s.Data.Projects().Get(idOrName); err == nil {
		return p, nil
	}
	p, err := s.Data.Projects().GetByName(idOrName)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// AnalyzersFor 解析出某个项目要用的分析器集合
func (s *Service) AnalyzersFor(p *model.Project) ([]analyzer.Analyzer, error) {
	names := []string(p.DefaultAnalyzers)
	if len(names) == 0 {
		names = s.Cfg.Scan.EnabledAnalyzers
	}
	return s.Registry.New(names)
}

// ---------------------------------------------------------------------------
// 分析
// ---------------------------------------------------------------------------

// StartAnalysisInput 描述一次请求的扫描
type StartAnalysisInput struct {
	ProjectID string   `json:"projectId" binding:"required"`
	Trigger   string   `json:"trigger"`
	Branch    string   `json:"branch"`
	Analyzers []string `json:"analyzers"`
}

// StartAnalysis 创建分析记录并把它排入队列
//
// 记录在入队之前创建，接口因此能立刻返回 ID，界面也能轮询进度，即使 worker
// 还没取走它
func (s *Service) StartAnalysis(ctx context.Context, in StartAnalysisInput) (*model.Analysis, error) {
	project, err := s.Data.Projects().Get(in.ProjectID)
	if err != nil {
		return nil, err
	}

	// 现在就解析分析器集合，非法的名字因此在请求时就失败
	analyzers, err := s.Registry.New(pickAnalyzers(in.Analyzers, project.DefaultAnalyzers, s.Cfg.Scan.EnabledAnalyzers))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	names := make([]string, 0, len(analyzers))
	for _, a := range analyzers {
		names = append(names, a.Name())
	}

	trigger := in.Trigger
	if trigger == "" {
		trigger = model.TriggerManual
	}
	branch := in.Branch
	if branch == "" {
		branch = project.Branch
	}

	analysis := &model.Analysis{
		ID:        model.NewID("ana"),
		ProjectID: project.ID,
		Status:    model.StatusPending,
		Trigger:   trigger,
		Branch:    branch,
		Path:      project.LocalPath,
		Analyzers: names,
		Progress:  0,
		StartedAt: time.Now(),
		CreatedAt: time.Now(),
	}
	if err := s.Data.Analyses().Create(analysis); err != nil {
		return nil, err
	}

	if s.Enqueuer == nil {
		return nil, fmt.Errorf("no task queue configured")
	}
	if err := s.Enqueuer.Enqueue(analysis.ID); err != nil {
		// 把记录标为失败，而不是让它永远停在待处理
		analysis.Status = model.StatusFailed
		analysis.Error = "failed to enqueue: " + err.Error()
		_ = s.Data.Analyses().Update(analysis)
		return nil, fmt.Errorf("enqueue analysis: %w", err)
	}

	s.Log.Info("analysis queued", "id", analysis.ID, "project", project.Name, "analyzers", names)
	return analysis, nil
}

// RunAnalysis 同步执行一次分析，worker、命令行和测试使用。HTTP 接口一律走
// StartAnalysis
func (s *Service) RunAnalysis(ctx context.Context, analysisID string) error {
	if s.Engine == nil {
		return fmt.Errorf("analysis engine is not configured")
	}
	return s.Engine.Run(ctx, analysisID)
}

// GetAnalysis 按 ID 返回分析
func (s *Service) GetAnalysis(ctx context.Context, id string) (*model.Analysis, error) {
	return s.Data.Analyses().Get(id)
}

// EngineProgress 报告运行中分析的实时进度
//
// 进度放在内存而不是数据库，为每个分析器写一行会造成写竞争，而这些信息在运行
// 结束后不再有用
func (s *Service) EngineProgress(analysisID string) Progress {
	if s.Engine == nil {
		return Progress{}
	}
	percent, stage := s.Engine.Progress(analysisID)
	return Progress{Percent: percent, Stage: stage}
}

// Progress 是运行中分析的实时状态
type Progress struct {
	Percent int    `json:"percent"`
	Stage   string `json:"stage"`
}

// ListAnalyses 返回项目的历史记录，最新的在前
func (s *Service) ListAnalyses(ctx context.Context, projectID string, limit int) ([]model.Analysis, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.Data.Analyses().ListByProject(projectID, limit)
}

// Trend 返回用于绘图的评分历史
func (s *Service) Trend(ctx context.Context, projectID string, limit int) ([]TrendPoint, error) {
	if limit <= 0 {
		limit = 30
	}
	analyses, err := s.Data.Analyses().Trend(projectID, limit)
	if err != nil {
		return nil, err
	}

	out := make([]TrendPoint, 0, len(analyses))
	for _, a := range analyses {
		out = append(out, TrendPoint{
			AnalysisID: a.ID,
			At:         a.CreatedAt.Format(time.RFC3339),
			Score:      a.Score,
			Total:      a.TotalIssues,
			Critical:   a.CriticalIssues,
			Errors:     a.ErrorIssues,
			Warnings:   a.WarningIssues,
			GateStatus: a.GateStatus,
		})
	}
	return out, nil
}

// TrendPoint 是评分历史图上的一个点
type TrendPoint struct {
	AnalysisID string  `json:"analysisId"`
	At         string  `json:"at"`
	Score      float64 `json:"score"`
	Total      int     `json:"total"`
	Critical   int     `json:"critical"`
	Errors     int     `json:"errors"`
	Warnings   int     `json:"warnings"`
	GateStatus string  `json:"gateStatus"`
}

// BuildReport 汇总一次分析的全部内容，供导出使用
//
// 放在 service 而不是命令行里，是因为导出有命令行和面板两条入口，内容必须一致，
// 同一次扫描从哪边导出都得到同一份文件。这段逻辑原先只写在 cli.go 里，面板用不到
//
// 问题不设分页，报告要的是全貌，翻页在这里没有意义
func (s *Service) BuildReport(ctx context.Context, projectID, analysisID string) (*report.Report, error) {
	project, err := s.ResolveProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	analysis, err := s.Data.Analyses().Get(analysisID)
	if err != nil {
		return nil, err
	}
	// 一次分析只能属于一个项目，跨项目取等于给了个不存在的组合
	if analysis.ProjectID != project.ID {
		return nil, fmt.Errorf("%w: analysis %s does not belong to project %s", ErrInvalid, analysisID, project.ID)
	}

	issues, err := s.Data.Issues().ListByAnalysis(analysisID, store.IssueFilter{})
	if err != nil {
		return nil, err
	}

	out := &report.Report{
		Project:     *project,
		Analysis:    *analysis,
		Issues:      issues,
		Stats:       report.BuildStats(issues),
		GeneratedAt: time.Now(),
		Version:     Version,
	}

	// 趋势是报告里的次要内容，取不到就不带，不该让整份导出失败
	if trend, err := s.Trend(ctx, project.ID, 30); err == nil {
		for _, t := range trend {
			out.Trend = append(out.Trend, report.TrendPoint{
				At:         t.At,
				Score:      t.Score,
				Total:      t.Total,
				GateStatus: t.GateStatus,
			})
		}
	}

	return out, nil
}

// ListIssues 返回某次分析的问题，并应用过滤条件
func (s *Service) ListIssues(ctx context.Context, analysisID string, filter store.IssueFilter) ([]model.Issue, int64, error) {
	total, err := s.Data.Issues().CountByAnalysis(analysisID, filter)
	if err != nil {
		return nil, 0, err
	}
	issues, err := s.Data.Issues().ListByAnalysis(analysisID, filter)
	if err != nil {
		return nil, 0, err
	}
	return issues, total, nil
}

// GetIssue 返回单条问题
func (s *Service) GetIssue(ctx context.Context, id string) (*model.Issue, error) {
	return s.Data.Issues().Get(id)
}

// SetIssueStatus 记录一次分诊决定（忽略 / 误报）
func (s *Service) SetIssueStatus(ctx context.Context, id, status string) (*model.Issue, error) {
	switch status {
	case model.IssueStatusOpen, model.IssueStatusIgnored, model.IssueStatusFalsePositive:
	default:
		return nil, fmt.Errorf("%w: status must be open, ignored or false_positive", ErrInvalid)
	}

	issue, err := s.Data.Issues().Get(id)
	if err != nil {
		return nil, err
	}
	issue.Status = status
	if err := s.Data.Issues().Update(issue); err != nil {
		return nil, err
	}
	return issue, nil
}

// IssueStats 是某次分析的问题汇总
type IssueStats struct {
	Total      int              `json:"total"`
	BySeverity map[string]int   `json:"bySeverity"`
	ByAnalyzer map[string]int   `json:"byAnalyzer"`
	TopRules   []store.RuleStat `json:"topRules"`
}

// StatsForAnalysis 汇总某次分析的问题
func (s *Service) StatsForAnalysis(ctx context.Context, analysisID string) (IssueStats, error) {
	out := IssueStats{
		BySeverity: map[string]int{},
		ByAnalyzer: map[string]int{},
	}

	bySeverity, err := s.Data.Issues().CountBySeverity(analysisID)
	if err != nil {
		return out, err
	}
	for _, sev := range model.AllSeverities {
		out.BySeverity[string(sev)] = bySeverity[sev]
		out.Total += bySeverity[sev]
	}

	if out.ByAnalyzer, err = s.Data.Issues().CountByAnalyzer(analysisID); err != nil {
		return out, err
	}
	if out.TopRules, err = s.Data.Issues().CountByRule(analysisID, 15); err != nil {
		return out, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 质量门禁
// ---------------------------------------------------------------------------

// GetGate 返回项目的门禁，回退到全局默认值
func (s *Service) GetGate(ctx context.Context, projectID string) (*model.QualityGate, error) {
	gate, err := s.Data.QualityGates().Get(projectID)
	if err == nil {
		return gate, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	// 从配置临时推导出一个门禁，界面因此总有值可显示
	return &model.QualityGate{
		ID:            "default",
		ProjectID:     projectID,
		MinScore:      s.Cfg.Quality.MinScore,
		MaxCritical:   s.Cfg.Quality.MaxCritical,
		MaxHigh:       s.Cfg.Quality.MaxHigh,
		MaxComplexity: s.Cfg.Quality.MaxComplexity,
		MinCoverage:   s.Cfg.Quality.MinCoverage,
		Enabled:       true,
	}, nil
}

// SaveGate 持久化门禁
func (s *Service) SaveGate(ctx context.Context, projectID string, in model.QualityGate) (*model.QualityGate, error) {
	if _, err := s.Data.Projects().Get(projectID); err != nil {
		return nil, err
	}

	if in.MinScore < 0 || in.MinScore > 100 {
		return nil, fmt.Errorf("%w: minScore must be between 0 and 100", ErrInvalid)
	}
	if in.MinCoverage < 0 || in.MinCoverage > 100 {
		return nil, fmt.Errorf("%w: minCoverage must be between 0 and 100", ErrInvalid)
	}
	if in.MaxCritical < 0 || in.MaxHigh < 0 || in.MaxComplexity < 0 {
		return nil, fmt.Errorf("%w: thresholds cannot be negative", ErrInvalid)
	}

	gate, err := s.Data.QualityGates().Get(projectID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		gate = &model.QualityGate{ID: model.NewID("gat"), ProjectID: projectID}
	}

	gate.MinScore = in.MinScore
	gate.MaxCritical = in.MaxCritical
	gate.MaxHigh = in.MaxHigh
	gate.MaxComplexity = in.MaxComplexity
	gate.MinCoverage = in.MinCoverage
	gate.FailOnWarning = in.FailOnWarning
	gate.Enabled = in.Enabled

	if err := s.Data.QualityGates().Save(gate); err != nil {
		return nil, err
	}
	return gate, nil
}

// GateFromModel 把存储的门禁转成求值器用的类型
func GateFromModel(g *model.QualityGate) quality.Gate {
	if g == nil {
		return quality.DefaultGate()
	}
	return quality.Gate{
		MinScore:      g.MinScore,
		MaxCritical:   g.MaxCritical,
		MaxHigh:       g.MaxHigh,
		MaxComplexity: g.MaxComplexity,
		MinCoverage:   g.MinCoverage,
		FailOnWarning: g.FailOnWarning,
		Enabled:       g.Enabled,
	}
}

// ---------------------------------------------------------------------------
// 规则
// ---------------------------------------------------------------------------

// ListRules 返回所有规则，内置的和自定义的
func (s *Service) ListRules(ctx context.Context) ([]model.Rule, error) {
	return s.Data.Rules().List()
}

// RuleInput 是创建或更新规则的入参
type RuleInput struct {
	Name     string   `json:"name" binding:"required"`
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	Advice   string   `json:"advice"`
	Patterns []string `json:"patterns"`
	Paths    []string `json:"paths"`
	Exclude  []string `json:"exclude"`
	Enabled  *bool    `json:"enabled"`
}

// CreateRule 添加一条自定义规则
func (s *Service) CreateRule(ctx context.Context, in RuleInput) (*model.Rule, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if len(in.Patterns) == 0 {
		return nil, fmt.Errorf("%w: at least one pattern is required", ErrInvalid)
	}
	if _, err := s.Data.Rules().GetByName(name); err == nil {
		return nil, fmt.Errorf("%w: a rule named %q already exists", ErrConflict, name)
	}

	// 严格解析，ParseSeverity 从不失败，旧的 Valid() 检查因此是死代码，
	// severity: "banana" 会被静默存成 warning。悄悄跑在错误等级上的规则比拒绝保存
	// 更糟
	severity := model.SeverityWarning
	if in.Severity != "" {
		parsed, err := model.ParseSeverityStrict(in.Severity)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		severity = parsed
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	rule := &model.Rule{
		ID:        model.NewID("rul"),
		Name:      name,
		Analyzer:  "custom",
		Severity:  severity,
		Message:   in.Message,
		Advice:    in.Advice,
		Patterns:  in.Patterns,
		Paths:     defaultPaths(in.Paths),
		Exclude:   in.Exclude,
		Enabled:   enabled,
		Builtin:   false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// 先编译再落库，非法的正则绝不能进数据库
	if err := validateRuleCompiles(rule); err != nil {
		return nil, err
	}
	if err := s.Data.Rules().Create(rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// UpdateRule 编辑已有规则
func (s *Service) UpdateRule(ctx context.Context, id string, in RuleInput) (*model.Rule, error) {
	rule, err := s.Data.Rules().Get(id)
	if err != nil {
		return nil, err
	}

	if in.Severity != "" {
		parsed, err := model.ParseSeverityStrict(in.Severity)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		rule.Severity = parsed
	}
	if in.Message != "" {
		rule.Message = in.Message
	}
	if in.Advice != "" {
		rule.Advice = in.Advice
	}
	if len(in.Patterns) > 0 {
		rule.Patterns = in.Patterns
	}
	if in.Paths != nil {
		rule.Paths = in.Paths
	}
	if in.Exclude != nil {
		rule.Exclude = in.Exclude
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	if in.Name != "" && in.Name != rule.Name {
		if rule.Builtin {
			return nil, fmt.Errorf("%w: built-in rules cannot be renamed", ErrInvalid)
		}
		if existing, err := s.Data.Rules().GetByName(in.Name); err == nil && existing.ID != id {
			return nil, fmt.Errorf("%w: a rule named %q already exists", ErrConflict, in.Name)
		}
		rule.Name = in.Name
	}

	if err := validateRuleCompiles(rule); err != nil {
		return nil, err
	}
	if err := s.Data.Rules().Update(rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// DeleteRule 删除一条自定义规则。内置规则只能停用，保留它们让全新安装总有可用的基线
func (s *Service) DeleteRule(ctx context.Context, id string) error {
	rule, err := s.Data.Rules().Get(id)
	if err != nil {
		return err
	}
	if rule.Builtin {
		return fmt.Errorf("%w: built-in rules cannot be deleted; disable it instead", ErrInvalid)
	}
	return s.Data.Rules().Delete(id)
}

// ToggleRule 翻转规则的启用状态
func (s *Service) ToggleRule(ctx context.Context, id string, enabled bool) (*model.Rule, error) {
	rule, err := s.Data.Rules().Get(id)
	if err != nil {
		return nil, err
	}
	rule.Enabled = enabled
	if err := s.Data.Rules().Update(rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// ---------------------------------------------------------------------------
// 系统
// ---------------------------------------------------------------------------

// SystemInfo 汇总服务器信息，供关于页面和命令行使用
type SystemInfo struct {
	Version      string                         `json:"version"`
	Mode         string                         `json:"mode"`
	Database     string                         `json:"database"`
	Workspace    string                         `json:"workspace"`
	ConfigFile   string                         `json:"configFile"`
	Running      int                            `json:"runningTasks"`
	Analyzers    []analyzer.AnalyzerReadiness   `json:"analyzers"`
	Tools        map[string]analyzer.ToolStatus `json:"tools"`
	RuleCount    int                            `json:"ruleCount"`
	ProjectCount int                            `json:"projectCount"`
	// Offline 表示分析器是否被禁止联网
	//
	// 暴露它是为了让面板显示该设置，而不是让用户去猜为什么漏洞扫描什么都没返回
	Offline bool `json:"offline"`
	// AnalyzerOptions 是每个分析器每个键的配置值，缺席的键表示使用该分析器声明的默认值
	AnalyzerOptions map[string]map[string]string `json:"analyzerOptions,omitempty"`
	// MinScore 是质量门禁的分数下限，供面板绘制门禁线
	//
	// 暴露它不是为了在面板里重复判定逻辑，而是让门禁线跟随配置。项目页和总览页
	// 画同一个数字时读同一份来源，才不会互相矛盾
	MinScore float64 `json:"minScore"`
}

// SetOffline 开关后续扫描的网络访问
//
// 做成运行时可改，而不只是配置文件，因为正确答案取决于机器当下的连通性，
// 为了翻一个标志重启服务器并不划算
func (s *Service) SetOffline(ctx context.Context, offline bool) {
	s.Cfg.Scan.Offline = offline
	s.Log.Info("offline mode changed", "offline", offline)
}

// SetAnalyzerOptions 替换某个分析器的设置
//
// 整份映射替换而不是合并，面板发送的是控件当前显示的内容，合并会让一个选项
// 无法被关掉，因为缺席的键会保留旧值
//
// 选项对照分析器声明的结构校验，未知的键直接拒绝。不起作用的设置比失败的设置更糟，
// 用户拨了开关却不知道它没生效
func (s *Service) SetAnalyzerOptions(ctx context.Context, analyzerName string, options map[string]string) error {
	an, err := s.analyzerByName(analyzerName)
	if err != nil {
		return err
	}

	provider, ok := an.(analyzer.OptionProvider)
	if !ok {
		return fmt.Errorf("%w: analyzer %q has no configurable options", ErrInvalid, analyzerName)
	}

	known := map[string]bool{}
	for _, opt := range provider.Options() {
		known[opt.Key] = true
	}
	for key := range options {
		if !known[key] {
			return fmt.Errorf("%w: analyzer %q has no option %q", ErrInvalid, analyzerName, key)
		}
	}

	if s.Cfg.Scan.AnalyzerOptions == nil {
		s.Cfg.Scan.AnalyzerOptions = map[string]map[string]string{}
	}
	if len(options) == 0 {
		// 空映射表示「回到默认值」，那是条目的缺席，而不是一个装满空字符串的
		// 映射
		delete(s.Cfg.Scan.AnalyzerOptions, analyzerName)
	} else {
		s.Cfg.Scan.AnalyzerOptions[analyzerName] = options
	}

	s.Log.Info("analyzer options changed", "analyzer", analyzerName, "options", options)
	return nil
}

// analyzerByName 实例化一个已注册的分析器
func (s *Service) analyzerByName(name string) (analyzer.Analyzer, error) {
	if s.Registry == nil {
		return nil, fmt.Errorf("%w: no analyzer registry", ErrInvalid)
	}
	instances, err := s.Registry.New([]string{name})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("%w: analyzer %q not found", ErrInvalid, name)
	}
	return instances[0], nil
}

// Version 在构建时通过 -ldflags 写入
var Version = "dev"

// Info 汇总服务器状态
func (s *Service) Info(ctx context.Context) SystemInfo {
	info := SystemInfo{
		Version:         Version,
		Mode:            s.Cfg.Server.Mode,
		Database:        s.Cfg.Database.DSN,
		Workspace:       s.Cfg.Workspace.Root,
		ConfigFile:      s.Cfg.Source,
		Tools:           s.Avail.Snapshot(),
		Offline:         s.Cfg.Scan.Offline,
		AnalyzerOptions: s.Cfg.Scan.AnalyzerOptions,
		MinScore:        s.Cfg.Quality.MinScore,
	}
	info.Analyzers = analyzer.Readiness(s.Registry, s.Avail)
	if s.Enqueuer != nil {
		info.Running = s.Enqueuer.Running()
	}
	if n, err := s.Data.Rules().Count(); err == nil {
		info.RuleCount = int(n)
	}
	if projects, err := s.Data.Projects().List(); err == nil {
		info.ProjectCount = len(projects)
	}
	return info
}

// ScanPreflight 报告一次扫描启动前用户需要知道的事
//
// 只回答一个问题：这个项目该跑的分析器里，有几个因为工具缺失跑不了
//
// 「该跑」按项目语言算。一台机器上的分析器覆盖各种语言，不做这层过滤，一个
// JavaScript 项目会被告知缺十一个 Go / Rust / Python 工具，把真正缺的那个淹掉
//
// Analyzers 是可用的那些，Blocked 是无法运行的，每项带着分析器自己声明的安装方法
func (s *Service) ScanPreflight(ctx context.Context, projectID string) (*ScanPreflight, error) {
	project, err := s.ResolveProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	lang := project.Language
	readiness := analyzer.ReadinessInProject(s.Registry, s.Avail, lang, project.LocalPath)

	out := &ScanPreflight{
		Project:   project.Name,
		Language:  lang,
		Analyzers: make([]string, 0, len(readiness)),
		Blocked:   make([]BlockedAnalyzer, 0),
	}
	for _, item := range readiness {
		if item.Ready {
			out.Analyzers = append(out.Analyzers, item.Name)
			continue
		}
		// 从未探测过的不算缺失，那是接线问题，混进提醒会让用户去装一个可能已装好的工具
		if item.ProbeMissing {
			continue
		}
		out.Blocked = append(out.Blocked, BlockedAnalyzer{
			Name:         item.Name,
			Description:  item.Description,
			MissingTools: item.MissingTools,
			InstallHint:  item.InstallHint,
		})
	}
	return out, nil
}

// ScanPreflight 是一次扫描的启动前检查结果
type ScanPreflight struct {
	Project  string `json:"project"`
	Language string `json:"language"`
	// Analyzers 是本次会运行的分析器名字
	Analyzers []string `json:"analyzers"`
	// Blocked 是应该运行、但当前跑不了的
	Blocked []BlockedAnalyzer `json:"blocked"`
}

// BlockedAnalyzer 是一个因工具缺失而无法运行的分析器
type BlockedAnalyzer struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// MissingTools 是具体缺哪些可执行文件
	MissingTools []string `json:"missingTools"`
	// InstallHint 由分析器自己声明，与 MissingTools 配对给出，调用方因此不必
	// 从工具名反推命令
	InstallHint string `json:"installHint,omitempty"`
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func pickAnalyzers(requested, projectDefault, global []string) []string {
	switch {
	case len(requested) > 0:
		return requested
	case len(projectDefault) > 0:
		return projectDefault
	default:
		return global
	}
}

func defaultColor(c string) string {
	if strings.TrimSpace(c) == "" {
		return "#3b82f6"
	}
	return c
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func defaultPaths(paths []string) []string {
	if len(paths) == 0 {
		return []string{"**/*.go"}
	}
	return paths
}
