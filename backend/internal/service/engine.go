package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rolingg/fizzra/backend/internal/analyzer"
	"github.com/rolingg/fizzra/backend/internal/config"
	"github.com/rolingg/fizzra/backend/internal/model"
	"github.com/rolingg/fizzra/backend/internal/quality"
	"github.com/rolingg/fizzra/backend/internal/rule"
	"github.com/rolingg/fizzra/backend/internal/store"
)

// Engine 执行一次完整的分析，依次加载项目、准备源码、运行分析器、聚合发现、
// 评分、评估门禁并落库
//
// 单次调用是同步的，状态只属于这一次分析，任务队列可以并发跑多个实例而无需协调
type Engine struct {
	Data     store.Data
	Cfg      *config.Config
	Log      *slog.Logger
	Registry *analyzer.Registry

	// ruleMu 保护 ruleEngine 与 ruleEngineLang
	ruleMu     sync.RWMutex
	ruleEngine *rule.Engine
	// ruleEngineLang 是 ruleEngine 编译时所用的语言
	//
	// 规则引擎的注释、字符串和循环作用域过滤器都依赖具体语言的语法，所以每种语言
	// 各需一个实例。把语言标识和引擎放在一起，缓存命中时才能判断它是否真的适用于
	// 当前项目
	ruleEngineLang string

	// progress 和 stage 按分析 ID 记录进度与当前步骤，供界面在长时间扫描中轮询，
	// 避免反复读数据库
	progressMu sync.RWMutex
	progress   map[string]int
	stage      map[string]string

	// Events 广播扫描状态变化，界面据此实时更新
	Events *EventHub
}

// NewEngine 构建分析引擎
func NewEngine(data store.Data, cfg *config.Config, log *slog.Logger, registry *analyzer.Registry) *Engine {
	return &Engine{
		Data:     data,
		Cfg:      cfg,
		Log:      log,
		Registry: registry,
		progress: map[string]int{},
		stage:    map[string]string{},
		Events:   NewEventHub(),
	}
}

// ReloadRules 从数据库重建规则引擎。启动时调用，规则增删改或启停后也要调用
//
// 重建的是 Go 语法的引擎，扫描其他语言时会按需切换，见 ruleEngineForLanguage
func (e *Engine) ReloadRules() error {
	return e.reloadRulesForLanguage(model.LanguageGo)
}

// reloadRulesForLanguage 用某语言的语法编译已启用的规则
func (e *Engine) reloadRulesForLanguage(language string) error {
	rules, err := e.Data.Rules().ListEnabled()
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}

	engine := rule.New(rules,
		rule.WithMinSeverity(model.SeverityInfo),
		rule.WithLanguage(language),
	)
	for _, skipped := range engine.Skipped() {
		e.Log.Warn("rule skipped: invalid pattern", "err", skipped)
	}

	e.ruleMu.Lock()
	e.ruleEngine = engine
	e.ruleEngineLang = language
	e.ruleMu.Unlock()

	e.Log.Info("rule engine reloaded", "rules", engine.Len(), "language", language)
	return nil
}

// ruleEngineForLanguage 返回适配该语言的规则引擎，缓存与语言一致时直接复用
//
// 过滤器依赖具体语法，在 Python 项目上沿用 Go 编译的引擎会出错：Python 没有花括号，
// 循环嵌套永远不收敛，第一个 for 之后的每一行都会被判为循环体内
//
// 编译期间持写锁，一次分析不会中途更换语法。只有语言与缓存不同才会重新编译
func (e *Engine) ruleEngineForLanguage(language string) (*rule.Engine, error) {
	if language == "" {
		language = model.LanguageGo
	}

	e.ruleMu.RLock()
	cached, cachedLang := e.ruleEngine, e.ruleEngineLang
	e.ruleMu.RUnlock()

	if cached != nil && cachedLang == language {
		return cached, nil
	}

	if err := e.reloadRulesForLanguage(language); err != nil {
		return nil, err
	}

	e.ruleMu.RLock()
	defer e.ruleMu.RUnlock()
	return e.ruleEngine, nil
}

// ruleAdapter 把 rule.Engine 适配成分析器的 RuleEvaluator，分析器包因此不必
// 依赖 rule 和 model
type ruleAdapter struct{ inner *rule.Engine }

func (a ruleAdapter) Evaluate(relPath string, content []byte) []analyzer.RuleMatch {
	matches := a.inner.Evaluate(relPath, content)
	out := make([]analyzer.RuleMatch, 0, len(matches))
	for _, m := range matches {
		out = append(out, analyzer.RuleMatch{
			Rule:     m.Rule,
			Severity: string(m.Severity),
			Message:  m.Message,
			Advice:   m.Advice,
			Fix: analyzer.FixExample{
				Before:   m.Fix.Before,
				After:    m.Fix.After,
				Note:     m.Fix.Note,
				Language: m.Fix.Language,
			},
			File:   m.File,
			Line:   m.Line,
			Column: m.Column,
			Code:   m.Code,
		})
	}
	return out
}

func (a ruleAdapter) Len() int { return a.inner.Len() }

func (a ruleAdapter) Skipped() []error { return a.inner.Skipped() }

func (a ruleAdapter) AppliesTo(relPath string) bool { return a.inner.AppliesTo(relPath) }

// Progress 返回某次分析当前的百分比和所处步骤
func (e *Engine) Progress(analysisID string) (int, string) {
	e.progressMu.RLock()
	defer e.progressMu.RUnlock()
	return e.progress[analysisID], e.stage[analysisID]
}

func (e *Engine) setProgress(analysisID, projectID string, percent int, stage string) {
	e.progressMu.Lock()
	e.progress[analysisID] = percent
	e.stage[analysisID] = stage
	e.progressMu.Unlock()

	e.Events.Publish(ScanEvent{
		Type:       "progress",
		AnalysisID: analysisID,
		ProjectID:  projectID,
		Status:     model.StatusRunning,
		Progress:   percent,
		Stage:      stage,
	})
}

func (e *Engine) clearProgress(analysisID string) {
	e.progressMu.Lock()
	delete(e.progress, analysisID)
	delete(e.stage, analysisID)
	e.progressMu.Unlock()
}

// RunAnalysis 实现任务队列的 Runner 接口，是对 Run 的薄包装，队列因此只依赖
// 单个方法
func (e *Engine) RunAnalysis(ctx context.Context, analysisID string) error {
	return e.Run(ctx, analysisID)
}

// Run 执行 analysisID 所指的那次分析
func (e *Engine) Run(ctx context.Context, analysisID string) error {
	analysis, err := e.Data.Analyses().Get(analysisID)
	if err != nil {
		return fmt.Errorf("load analysis %s: %w", analysisID, err)
	}
	project, err := e.Data.Projects().Get(analysis.ProjectID)
	if err != nil {
		return fmt.Errorf("load project %s: %w", analysis.ProjectID, err)
	}

	started := time.Now()
	analysis.Status = model.StatusRunning
	analysis.StartedAt = started
	analysis.Progress = 0
	if err := e.Data.Analyses().Update(analysis); err != nil {
		return err
	}
	e.setProgress(analysisID, project.ID, 0, "starting")
	defer e.clearProgress(analysisID)

	log := e.Log.With("analysis", analysis.ID, "project", project.Name)

	finish := func(a *model.Analysis, runErr error) error {
		a.FinishedAt = new(time.Now())
		a.DurationMS = time.Since(started).Milliseconds()
		a.Progress = 100
		if runErr != nil {
			a.Status = model.StatusFailed
			a.Error = failureDetail(runErr)
		} else {
			a.Status = model.StatusSuccess
		}
		e.setProgress(analysisID, project.ID, 100, "done")
		if err := e.Data.Analyses().Update(a); err != nil {
			log.Error("failed to persist analysis", "err", err)
		}
		// 广播完成事件，界面据此刷新项目状态
		e.Events.Publish(ScanEvent{
			Type:       "finished",
			AnalysisID: analysisID,
			ProjectID:  project.ID,
			Status:     a.Status,
			Progress:   100,
			Stage:      "done",
		})
		return runErr
	}

	// 整次运行的预算，卡住的外部工具不能长期占用 worker
	runCtx, cancel := context.WithTimeout(ctx, e.Cfg.Scan.Timeout)
	defer cancel()

	e.setProgress(analysisID, project.ID, 5, "preparing sources")
	projectPath, err := e.prepareSources(runCtx, project, log)
	if err != nil {
		return finish(analysis, fmt.Errorf("prepare sources: %w", err))
	}
	analysis.Path = projectPath

	e.setProgress(analysisID, project.ID, 10, "indexing files")
	fileSet, err := analyzer.NewFileSet(projectPath)
	if err != nil {
		return finish(analysis, fmt.Errorf("index project files: %w", err))
	}
	log.Info("project indexed", "files", len(fileSet.All()))

	runner := analyzer.NewExecRunner(e.Log, e.Cfg.Scan.PerToolTimeout)

	// 规则引擎必须适配本项目的语言，否则文档字符串里的匹配和循环作用域都会算错
	engine, err := e.ruleEngineForLanguage(project.Language)
	if err != nil {
		return finish(analysis, err)
	}

	actx := &analyzer.Context{
		Context:         runCtx,
		ProjectPath:     projectPath,
		ProjectName:     project.Name,
		ProjectKind:     project.Kind,
		ProjectLanguage: project.Language,
		Root:            e.Cfg.Workspace.Root,
		Log:             log,
		ToolTimeout:     e.Cfg.Scan.PerToolTimeout,
		Runner:          runner,
		Files:           fileSet,
		Offline:         e.Cfg.Scan.Offline,
		Options: func(analyzerName, key string) (string, bool) {
			opts, ok := e.Cfg.Scan.AnalyzerOptions[analyzerName]
			if !ok {
				return "", false
			}
			value, ok := opts[key]
			return value, ok
		},
	}
	if engine != nil {
		actx.Rules = ruleAdapter{inner: engine}
	}

	analyzers, excluded, err := e.resolveAnalyzers(analysis, project.Language)
	if err != nil {
		// 记下为什么什么都没跑。只返回错误会把分析标为失败却丢掉细节，而没有原因的
		// 失败与崩溃无法区分。读者需要知道分析器存在，只是都不适用于这门语言
		if skipped := e.Registry.SkippedForLanguage(project.Language); len(skipped) > 0 {
			analysis.Incomplete = true
			analysis.NoAnalyzerRan = true
			analysis.SkippedAnalyzers = skipped
			analysis.SkipReasons = []string{err.Error()}
		}
		return finish(analysis, err)
	}

	results, metrics := e.runAnalyzers(actx, analyzers, analysisID, project.ID)

	// 被语言过滤掉的名字并入 results，下面的跳过统计只需遍历一份列表。它们标记为
	// Skipped 且不携带发现，不影响评分和问题列表
	results = append(results, excluded...)

	e.setProgress(analysisID, project.ID, 80, "saving results")
	merged := analyzer.Aggregate(results)

	// 落库前对照项目历史标记新增与既有
	baseline, err := e.Data.Issues().FingerprintsByProject(project.ID, 20)
	if err != nil {
		log.Warn("could not load issue baseline", "err", err)
		baseline = map[string]struct{}{}
	}

	// 非正的上限表示不限制，Engine 也可能被直接构造而不经过 config.Load
	maxIssues := e.Cfg.Scan.MaxIssues
	truncated := maxIssues > 0 && len(merged) > maxIssues
	if truncated {
		merged = merged[:maxIssues]
	}

	records := make([]model.Issue, 0, len(merged))
	for _, issue := range merged {
		records = append(records, e.toRecord(issue, analysis, project, baseline))
	}
	if err := e.Data.Issues().BatchCreate(records); err != nil {
		return finish(analysis, fmt.Errorf("save issues: %w", err))
	}

	e.setProgress(analysisID, project.ID, 92, "scoring")

	// 传递依赖的发现默认不计入评分
	//
	// 它们会照常报出，但项目无法直接升级它们，计入评分会让分数随上游发版节奏波动，
	// 而不是反映被度量代码的质量。需要更严格口径的项目可以打开
	// cargo-audit.scoredTransitive
	scoredIssues := records
	if strict, err := e.Cfg.AnalyzerFlag("cargo-audit", "scoredTransitive", false); err == nil && !strict {
		scoredIssues = make([]model.Issue, 0, len(records))
		for _, issue := range records {
			if issue.Origin == model.OriginTransitive {
				continue
			}
			scoredIssues = append(scoredIssues, issue)
		}
	}

	scoreInput := quality.Input{
		Issues:  scoredIssues,
		Metrics: metrics,
		Loc:     metrics.CodeLines,
	}
	scoreResult := quality.Score(scoreInput, quality.DefaultWeights(), quality.DefaultThresholds())

	gate, err := e.gateFor(project.ID)
	if err != nil {
		log.Warn("could not load quality gate, using defaults", "err", err)
		gate = quality.DefaultGate()
	}
	gateResult := quality.Evaluate(gate, scoreResult, scoreInput)

	analysis.Metrics = metrics
	analysis.Score = scoreResult.Score
	analysis.GateStatus = gateResult.Status
	analysis.GateReason = gateResult.Reason
	analysis.TotalIssues = len(records)
	analysis.CriticalIssues = countSeverity(records, model.SeverityCritical)
	analysis.ErrorIssues = countSeverity(records, model.SeverityError)
	analysis.WarningIssues = countSeverity(records, model.SeverityWarning)
	analysis.InfoIssues = countSeverity(records, model.SeverityInfo)
	analysis.Analyzers = analyzerNames(analyzers)

	// 运行本身成功时，Error 字段兼作诊断通道，记录被跳过的分析器
	if diag := summarizeDiagnostics(results); diag != "" {
		analysis.Error = diag
	}

	if err := e.Data.Projects().SetLastAnalysis(project.ID, analysis.ID); err != nil {
		log.Warn("could not update project last analysis", "err", err)
	}

	// 启动之后报错的分析器与没装工具不同，它的发现缺失，评分因证据不足而被抬高，
	// 这时报通过比不报更糟。已经开始失败的门禁保持原状态与原因，否决只作用于通过
	var failed []string
	var reasons []string
	var logParts []string
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		failed = append(failed, r.Analyzer)
		reasons = append(reasons, r.Err.Error())
		logParts = append(logParts, fmt.Sprintf("%s: %v", r.Analyzer, r.Err))
	}
	if len(failed) > 0 {
		analysis.Incomplete = true
		analysis.FailedAnalyzers = failed
		analysis.FailureReasons = reasons
		log.Warn("some analyzers failed", "failures", strings.Join(logParts, "; "))

		if gateResult.Passed {
			gateResult.Passed = false
			gateResult.Status = model.GateFailed
			gateResult.Reason = fmt.Sprintf(
				"%d analyzer(s) failed, so this scan is incomplete and cannot be trusted to pass. %s",
				len(failed), firstFailureDetail(failed))
			analysis.GateStatus = gateResult.Status
			analysis.GateReason = gateResult.Reason
		}
	}

	// 把每个分析器的结局归成三类：失败、跳过、完成
	//
	// 失败说明分析器坏了，跳过说明它正确地退出了，完成才可能算作检查过代码。
	// 指标分析器度量规模与重复度，找不出缺陷，所以单独归到 measured
	var skipped []string
	var skipReasons []string
	inspected, measured := 0, 0
	completed := 0
	for _, r := range results {
		switch {
		case r.Err != nil:
			// 失败的不算检查过，它没有产生可信的结论
			continue
		case r.Skipped:
			skipped = append(skipped, r.Analyzer)
			if r.SkipReason != "" {
				skipReasons = append(skipReasons, fmt.Sprintf("%s: %s", r.Analyzer, r.SkipReason))
			}
		default:
			completed++
			if findsProblems(r.Analyzer) {
				inspected++
			} else {
				measured++
			}
		}
	}
	// 只数成功跑完的，CI 的 --allow-skipped 靠它区分「度量跑了、检查跳过了」
	// 和「一个都没跑」
	analysis.AnalyzersCompleted = completed

	// 每次扫描都记录跳过，否则漏掉半数检查的扫描与跑全了却没问题的无法区分
	if len(skipped) > 0 {
		analysis.SkippedAnalyzers = skipped
		analysis.SkipReasons = skipReasons
		analysis.SkippedTools = dedupeTools(results)
	}

	// 没有任何分析器去找过问题，评分就不可信
	//
	// 有分析器失败时走上面那段，那里说的是「哪个分析器坏了」，读者要做的准备不同
	if inspected == 0 && len(skipped) > 0 && len(failed) == 0 {
		analysis.Incomplete = true
		analysis.NoAnalyzerRan = true
		log.Warn("no analyzer inspected the code", "skipped", strings.Join(skipped, ", "), "measuring", measured)

		if gateResult.Passed && measured == 0 {
			// 什么都没跑，分数没有依据
			gateResult.Passed = false
			gateResult.Status = model.GateFailed
			gateResult.Reason = fmt.Sprintf(
				"no analyzer ran, so nothing was checked and the score means nothing. "+
					"%d analyzer(s) skipped: %s",
				len(skipped), firstFailureDetail(skipped))
			analysis.GateStatus = gateResult.Status
			analysis.GateReason = gateResult.Reason
		} else if gateResult.Passed {
			// 有度量没有检查，分数只反映规模，不构成通过结论
			gateResult.Passed = false
			gateResult.Status = model.GateFailed
			gateResult.Reason = fmt.Sprintf(
				"no analyzer inspected the code, so this score reflects size and duplication only — "+
					"no bug, security or style check ran. %d analyzer(s) skipped: %s",
				len(skipped), firstFailureDetail(skipped))
			analysis.GateStatus = gateResult.Status
			analysis.GateReason = gateResult.Reason
		}
	}

	log.Info("analysis complete",
		"score", scoreResult.Score,
		"grade", scoreResult.Grade,
		"issues", len(records),
		"gate", gateResult.Status,
		"incomplete", analysis.Incomplete,
		"skipped", len(skipped),
		"duration", time.Since(started).Round(time.Millisecond),
		"truncated", truncated,
	)

	return finish(analysis, nil)
}

// failureDetail 返回给用户看的失败说明
//
// 命令失败时把完整 stderr 带上，不截断也不挑行：详情页有位置放，用户能自己
// 看出是哪一步、哪个原因。摘要式的短文对排查没用，反而要人再去找一次
func failureDetail(err error) string {
	var exitErr *analyzer.ExitError
	if errors.As(err, &exitErr) && exitErr.Stderr != "" && !exitErr.TimedOut {
		return err.Error() + "\n\n" + exitErr.Stderr
	}
	return err.Error()
}

// runAnalyzers 按配置的并发度并行执行分析器，返回各自的结果和指标快照
//
// 指标分析器通过访问器暴露输出而不是产出问题，所以在它跑完的那次调用里顺手取走
func (e *Engine) runAnalyzers(actx *analyzer.Context, analyzers []analyzer.Analyzer, analysisID, projectID string) ([]analyzer.AnalysisResult, model.Metrics) {
	concurrency := max(e.Cfg.Scan.Concurrency, 1)

	sem := make(chan struct{}, concurrency)
	results := make([]analyzer.AnalysisResult, len(analyzers))
	rawMetrics := make([]analyzer.MetricsSnapshot, len(analyzers))

	var wg sync.WaitGroup
	var done int
	var mu sync.Mutex

	// advance 记录一个分析器已经结束，跑完、跳过、取消都算
	// 进度按已结束数推进
	advance := func(name string) {
		mu.Lock()
		done++
		percent := 10 + int(float64(done)/float64(len(analyzers))*65)
		mu.Unlock()
		e.setProgress(analysisID, projectID, percent, name)
	}

	for i, a := range analyzers {
		wg.Add(1)
		go func(idx int, an analyzer.Analyzer) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-actx.Context.Done():
				results[idx] = analyzer.AnalysisResult{
					Analyzer: an.Name(),
					Err:      actx.Context.Err(),
				}
				advance(an.Name())
				return
			}

			start := time.Now()
			issues, err := analyzer.Invoke(an, actx)
			elapsed := time.Since(start)

			result := analyzer.AnalysisResult{Analyzer: an.Name(), Issues: issues, Err: err}

			// 指标分析器通过访问器发布快照，不是通过问题列表
			if reporter, ok := an.(analyzer.ResultReporter); ok && err == nil {
				rawMetrics[idx] = reporter.Metrics()
			}

			// 工具缺失记为跳过，不计为失败，扫描其余部分仍然有效
			if analyzer.IsToolUnavailable(err) {
				result.Err = nil
				result.Skipped = true
				result.SkipReason = err.Error()
				if d, ok := an.(analyzer.Describable); ok {
					result.SkippedTools = d.Requires()
				}
			}

			// 跑了却没有可检查的输入，同样是跳过而不是错误，但也不能算作覆盖，
			// 否则全部如此跳过的扫描会报出通过的评分
			if analyzer.IsNothingToCheck(err) {
				result.Err = nil
				result.Skipped = true
				result.SkipReason = err.Error()
			}

			results[idx] = result

			advance(an.Name())

			actx.Log.Info("analyzer finished",
				"analyzer", an.Name(),
				"issues", len(issues),
				"skipped", result.Skipped,
				"duration", elapsed.Round(time.Millisecond),
				"err", err,
			)
		}(i, a)
	}
	wg.Wait()

	return results, mergeMetrics(rawMetrics)
}

// mergeMetrics 取最丰富的那个快照。今天只有指标分析器会上报，但做合并能让引擎
// 在日后别的分析器也具备指标上报时依然正确
func mergeMetrics(snapshots []analyzer.MetricsSnapshot) model.Metrics {
	var best *analyzer.MetricsSnapshot
	for i := range snapshots {
		if snapshots[i].Files == 0 && snapshots[i].Lines == 0 {
			continue
		}
		if best == nil || snapshots[i].Files > best.Files {
			best = &snapshots[i]
		}
	}
	if best == nil {
		return model.Metrics{}
	}
	return toModelMetrics(*best)
}

// toModelMetrics 把分析器的快照映射到持久化类型
func toModelMetrics(s analyzer.MetricsSnapshot) model.Metrics {
	out := model.Metrics{
		Files:            s.Files,
		Lines:            s.Lines,
		CodeLines:        s.CodeLines,
		CommentLines:     s.CommentLines,
		BlankLines:       s.BlankLines,
		Functions:        s.Functions,
		AvgComplexity:    s.AvgComplexity,
		MaxComplexity:    s.MaxComplexity,
		Duplication:      s.Duplication,
		Coverage:         s.Coverage,
		TestFiles:        s.TestFiles,
		TestFunctions:    s.TestFunctions,
		ComplexFunctions: make([]model.ComplexFunction, 0, len(s.ComplexFunctions)),
		LongFunctions:    make([]model.ComplexFunction, 0, len(s.LongFunctions)),
		DuplicateBlocks:  make([]model.DuplicateBlock, 0, len(s.DuplicateBlocks)),
		LargestFiles:     make([]model.FileSize, 0, len(s.LargestFiles)),
	}
	for _, f := range s.ComplexFunctions {
		out.ComplexFunctions = append(out.ComplexFunctions, model.ComplexFunction(f))
	}
	for _, f := range s.LongFunctions {
		out.LongFunctions = append(out.LongFunctions, model.ComplexFunction(f))
	}
	for _, b := range s.DuplicateBlocks {
		out.DuplicateBlocks = append(out.DuplicateBlocks, model.DuplicateBlock(b))
	}
	for _, f := range s.LargestFiles {
		out.LargestFiles = append(out.LargestFiles, model.FileSize(f))
	}
	return out
}

// prepareSources 确保项目目录在本机可用，本地路径直接使用，仓库地址则克隆到工作区
// 并在后续扫描时拉取更新
func (e *Engine) prepareSources(ctx context.Context, project *model.Project, log *slog.Logger) (string, error) {
	if project.LocalPath != "" {
		if info, err := os.Stat(project.LocalPath); err == nil && info.IsDir() {
			return project.LocalPath, nil
		} else if err != nil {
			return "", fmt.Errorf("local path %s is not accessible: %w", project.LocalPath, err)
		}
	}

	if project.RepoURL == "" {
		return "", fmt.Errorf("project has neither a usable local path nor a repository URL")
	}

	dest := filepath.Join(e.Cfg.Workspace.Root, project.ID)
	if err := os.MkdirAll(e.Cfg.Workspace.Root, 0o750); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	runner := analyzer.NewExecRunner(log, 5*time.Minute)
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		log.Info("updating existing checkout", "path", dest)
		if _, err := runner.Run(ctx, dest, "git", "fetch", "--all", "--prune"); err != nil {
			log.Warn("git fetch failed, using existing checkout", "err", err)
		} else if _, err := runner.Run(ctx, dest, "git", "checkout", project.Branch); err != nil {
			log.Warn("git checkout failed, using current revision", "err", err)
		}
		return dest, nil
	}

	log.Info("cloning repository", "url", project.RepoURL, "dest", dest)
	args := []string{"clone", "--depth", "1"}
	if project.Branch != "" {
		args = append(args, "--branch", project.Branch)
	}
	args = append(args, project.RepoURL, dest)

	if _, err := runner.Run(ctx, e.Cfg.Workspace.Root, "git", args...); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	return dest, nil
}

// resolveAnalyzers 实例化分析上指定的分析器，只保留适用于该项目语言的
//
// 显式指定的列表同样要过滤，在 Rust 项目上固定 gofmt 不应让它运行
//
// 第二个返回值是因语言不符被丢弃的名字及原因。它们不会进入 results，跳过统计因此
// 看不到，若不带出去，被请求却被丢弃的分析器与从未被请求的无法区分
func (e *Engine) resolveAnalyzers(analysis *model.Analysis, lang string) ([]analyzer.Analyzer, []analyzer.AnalysisResult, error) {
	names := []string(analysis.Analyzers)
	if len(names) == 0 {
		names = e.Cfg.Scan.EnabledAnalyzers
	}
	analyzers, err := e.Registry.NewForLanguage(names, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve analyzers: %w", err)
	}

	// 记录哪些请求的名字活了下来，以便给被丢掉的那些命名
	kept := make(map[string]bool, len(analyzers))
	for _, a := range analyzers {
		kept[a.Name()] = true
	}
	var excluded []analyzer.AnalysisResult
	for _, name := range names {
		if kept[name] {
			continue
		}
		// 未知名字上面已由 NewForLanguage 报错，剩下的是因语言不符被丢弃的
		excluded = append(excluded, analyzer.AnalysisResult{
			Analyzer:   name,
			Skipped:    true,
			SkipReason: fmt.Sprintf("does not apply to %s projects", lang),
		})
	}

	if len(analyzers) == 0 {
		// 区分「一个都没选」与「全被语言过滤掉」，后者发生在该语言还没有分析器时，
		// 调用方据此报不完整而不是干净
		skipped := e.Registry.SkippedForLanguage(lang)
		if len(skipped) > 0 {
			return nil, nil, fmt.Errorf("no analyzers apply to language %q (registered: %v)", lang, skipped)
		}
		return nil, nil, fmt.Errorf("no analyzers selected")
	}
	return analyzers, excluded, nil
}

// toRecord 把分析器的问题转成可持久化的 model.Issue
func (e *Engine) toRecord(issue analyzer.Issue, analysis *model.Analysis, project *model.Project, baseline map[string]struct{}) model.Issue {
	file := strings.TrimPrefix(filepath.ToSlash(issue.File), "./")
	fingerprint := model.Fingerprint(issue.Analyzer, issue.Rule, file, issue.Message)

	_, existedBefore := baseline[fingerprint]

	record := model.Issue{
		ID:       model.NewID("iss"),
		Analysis: analysis.ID,
		Project:  project.ID,
		Analyzer: issue.Analyzer,
		Rule:     issue.Rule,
		Severity: model.ParseSeverity(issue.Severity),
		Message:  issue.Message,
		Advice:   issue.Advice,
		Fix: model.FixExample{
			Before:   issue.Fix.Before,
			After:    issue.Fix.After,
			Note:     issue.Fix.Note,
			Language: issue.Fix.Language,
		},
		File:        file,
		Line:        issue.Line,
		Column:      issue.Column,
		Code:        issue.Code,
		DocURL:      issue.DocURL,
		Fingerprint: fingerprint,
		IsNew:       !existedBefore,
		Status:      model.IssueStatusOpen,
		CreatedAt:   time.Now(),
		// Origin 用的是分析器包里那份常量副本，分析器不导入持久层模型，两边靠映射
		// 保持一致，漂移会在这里失配而不是拖到查询时才暴露
		Origin:         issue.Origin,
		DependencyPath: model.MultiString(issue.DependencyPath),
	}
	if issue.Severity == analyzer.SeverityCritical {
		record.Severity = model.SeverityCritical
	}
	return record.Normalized()
}

// collectMetrics 已并入 runAnalyzers，见 mergeMetrics

func (e *Engine) gateFor(projectID string) (quality.Gate, error) {
	gate, err := e.Data.QualityGates().Get(projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return quality.Gate{
				MinScore:      e.Cfg.Quality.MinScore,
				MaxCritical:   e.Cfg.Quality.MaxCritical,
				MaxHigh:       e.Cfg.Quality.MaxHigh,
				MaxComplexity: e.Cfg.Quality.MaxComplexity,
				MinCoverage:   e.Cfg.Quality.MinCoverage,
				Enabled:       true,
			}, nil
		}
		return quality.Gate{}, err
	}
	return quality.Gate{
		MinScore:      gate.MinScore,
		MaxCritical:   gate.MaxCritical,
		MaxHigh:       gate.MaxHigh,
		MaxComplexity: gate.MaxComplexity,
		MinCoverage:   gate.MinCoverage,
		FailOnWarning: gate.FailOnWarning,
		Enabled:       gate.Enabled,
	}, nil
}

// summarizeDiagnostics 为界面描述被跳过的分析器
func summarizeDiagnostics(results []analyzer.AnalysisResult) string {
	var skipped []string
	for _, r := range results {
		if r.Skipped {
			skipped = append(skipped, r.Analyzer)
		}
	}
	if len(skipped) == 0 {
		return ""
	}
	return "skipped analyzers (tool not installed): " + strings.Join(skipped, ", ")
}

func countSeverity(issues []model.Issue, sev model.Severity) int {
	n := 0
	for _, i := range issues {
		if i.Severity == sev {
			n++
		}
	}
	return n
}

func analyzerNames(analyzers []analyzer.Analyzer) []string {
	out := make([]string, 0, len(analyzers))
	for _, a := range analyzers {
		out = append(out, a.Name())
	}
	return out
}

// validateRuleCompiles 在正则进数据库之前先检查它们
func validateRuleCompiles(r *model.Rule) error {
	engine := rule.New([]model.Rule{*r})
	if skipped := engine.Skipped(); len(skipped) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, skipped[0].Error())
	}
	return nil
}

// ValidateRule 暴露模式校验，命令行 `fizzra rules check` 因此可以空跑一套规则
// 而不落任何数据
func (s *Service) ValidateRule(r model.Rule) (bool, error) {
	if err := validateRuleCompiles(&r); err != nil {
		return false, err
	}
	return true, nil
}

// findsProblems 报告某分析器能否找出缺陷
//
// 度量型分析器产出规模和重复度，评分要用，但它找不出缺陷，也不依赖外部工具，
// 从不跳过，算作覆盖会让检查全被跳过的项目照样报干净
//
// 按名字判断而不是靠分析器自报能力，是因为集合小而封闭，且这里判错就是门禁判错，
// 显式列表更容易审阅。新增度量型分析器必须加到这里，这正是重点
func findsProblems(analyzerName string) bool {
	switch analyzerName {
	case "metrics":
		return false
	default:
		return true
	}
}

// dedupeTools 汇总跳过原因里提到的缺失二进制
//
// 读者的下一步是安装它们，两个分析器缺同一个工具也只需装一次。顺序跟分析器列表，
// 不随 map 遍历顺序变化，多次运行结果稳定
func dedupeTools(results []analyzer.AnalysisResult) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range results {
		if !r.Skipped {
			continue
		}
		for _, tool := range r.SkippedTools {
			tool = strings.TrimSpace(tool)
			if tool == "" || seen[tool] {
				continue
			}
			seen[tool] = true
			out = append(out, tool)
		}
	}
	return out
}

// firstFailureDetail 只给出第一个阻塞项的名字
//
// 每个分析器的完整原因在 analysis.FailureReasons 和 SkipReasons 上，界面和命令行
// 已经逐条列出，门禁理由里再复述只会重复一遍而没有新信息
func firstFailureDetail(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("Blocking analyzer: %s.", names[0])
}
