package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/rolingg/fizzra/backend/internal/model"
	"github.com/rolingg/fizzra/backend/internal/report"
	"github.com/rolingg/fizzra/backend/internal/service"
	"github.com/rolingg/fizzra/backend/internal/store"
)

// ---------------------------------------------------------------------------
// 项目
// ---------------------------------------------------------------------------

func (h *Handler) listProjects(c *gin.Context) {
	summaries, err := h.Svc.ListProjects(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, summaries)
}

func (h *Handler) getProject(c *gin.Context) {
	project, err := h.Svc.ResolveProject(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, project)
}

func (h *Handler) createProject(c *gin.Context) {
	var in service.CreateProjectInput
	if err := c.ShouldBindJSON(&in); err != nil {
		h.badRequest(c, "invalid request body: "+err.Error())
		return
	}

	project, err := h.Svc.CreateProject(c.Request.Context(), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	created(c, project)
}

func (h *Handler) updateProject(c *gin.Context) {
	var in service.UpdateProjectInput
	if err := c.ShouldBindJSON(&in); err != nil {
		h.badRequest(c, "invalid request body: "+err.Error())
		return
	}

	project, err := h.Svc.UpdateProject(c.Request.Context(), c.Param("id"), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, project)
}

func (h *Handler) deleteProject(c *gin.Context) {
	if err := h.Svc.DeleteProject(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(204)
}

// maxBatchDelete 限制一次批量删除的项目数
//
// 目的是防误操作而不是限流，一个发错的请求不该能一次抹掉整个数据库。500 远高于
// 任何真实勾选场景，但挡得住「把所有 ID 都传进来」这种事故
const maxBatchDelete = 500

// deleteProjects 处理 DELETE /api/v1/projects，请求体是 ID 数组
// 返回 200 而不是 204，批量删除允许部分成功，调用方需要知道实际删掉了哪几个，
// 才能如实刷新列表
func (h *Handler) deleteProjects(c *gin.Context) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		h.badRequest(c, "invalid request body: "+err.Error())
		return
	}
	if len(body.IDs) == 0 {
		h.badRequest(c, "ids is required and must not be empty")
		return
	}
	// 重复 ID 由 service 去重，但上限要在去重前拦，否则重复项能绕过检查
	if len(body.IDs) > maxBatchDelete {
		h.badRequest(c, fmt.Sprintf("too many projects in one request (max %d)", maxBatchDelete))
		return
	}

	deleted, err := h.Svc.DeleteProjects(c.Request.Context(), body.IDs)
	if err != nil {
		// 部分删除时也要让调用方看到删到哪一步，所以连同已删列表一起返回
		c.JSON(http.StatusInternalServerError, Response{
			Data:  gin.H{"deleted": deleted},
			Error: &ErrorBody{Code: "internal_error", Message: err.Error()},
		})
		return
	}
	ok(c, gin.H{"deleted": deleted, "count": len(deleted)})
}

// projectOverview 一次往返就给出项目详情页需要的全部内容
func (h *Handler) projectOverview(c *gin.Context) {
	ctx := c.Request.Context()

	project, err := h.Svc.ResolveProject(ctx, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}

	overview := gin.H{"project": project}

	latest, err := h.Svc.Data.Analyses().LatestForProject(project.ID)
	if err == nil {
		overview["latest"] = latest

		stats, err := h.Svc.StatsForAnalysis(ctx, latest.ID)
		if err == nil {
			overview["issueStats"] = stats
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		h.fail(c, err)
		return
	}

	// 失败没有分数，但用户需要知道上次为什么没成，详情页据此展示完整原因
	if failed, err := h.Svc.Data.Analyses().LastFailedForProject(project.ID); err == nil {
		overview["lastFailed"] = failed
	} else if !errors.Is(err, store.ErrNotFound) {
		h.fail(c, err)
		return
	}

	if history, err := h.Svc.ListAnalyses(ctx, project.ID, 20); err == nil {
		overview["history"] = history
	}
	if trend, err := h.Svc.Trend(ctx, project.ID, 30); err == nil {
		overview["trend"] = trend
	}
	if gate, err := h.Svc.GetGate(ctx, project.ID); err == nil {
		overview["gate"] = gate
	}
	if stats, err := h.Svc.Data.Analyses().Stats(project.ID); err == nil {
		overview["stats"] = stats
	}

	ok(c, overview)
}

// ---------------------------------------------------------------------------
// 分析
// ---------------------------------------------------------------------------

func (h *Handler) startAnalysis(c *gin.Context) {
	var in service.StartAnalysisInput
	if err := c.ShouldBindJSON(&in); err != nil {
		h.badRequest(c, "invalid request body: "+err.Error())
		return
	}

	analysis, err := h.Svc.StartAnalysis(c.Request.Context(), in)
	if err != nil {
		// 队列满属于临时容量问题，不是输入有误，回 503 而不是 400
		if isQueueFull(err) {
			c.JSON(503, Response{Error: &ErrorBody{
				Code:    "queue_full",
				Message: err.Error(),
			}})
			return
		}
		h.fail(c, err)
		return
	}
	c.JSON(202, Response{Data: analysis})
}

func (h *Handler) getAnalysis(c *gin.Context) {
	analysis, err := h.Svc.GetAnalysis(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}

	payload := gin.H{"analysis": analysis}
	if analysis.Status == model.StatusRunning || analysis.Status == model.StatusPending {
		payload["progress"] = h.Svc.EngineProgress(analysis.ID)
	}
	ok(c, payload)
}

func (h *Handler) listAnalyses(c *gin.Context) {
	project, err := h.Svc.ResolveProject(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}

	limit := queryInt(c, "limit", 50)
	analyses, err := h.Svc.ListAnalyses(c.Request.Context(), project.ID, limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, analyses)
}

// exportReport 以附件形式下载某次扫描的报告，格式由 ?format= 选择
// 三种格式对应三类读者，html 用于阅读归档，json 交给其他程序处理，
// sarif 喂给 GitHub / GitLab 的代码扫描面板
// 语言取自 Accept-Language，报告是给人看的，界面语言就带在这个头里。
// 它不影响 JSON 和 SARIF，那两种格式不含本项目的界面文案
func (h *Handler) exportReport(c *gin.Context) {
	format, err := report.ParseFormat(c.DefaultQuery("format", "html"))
	if err != nil {
		h.badRequest(c, err.Error())
		return
	}

	project, err := h.Svc.ResolveProject(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}

	analysis, err := h.Svc.GetAnalysis(c.Request.Context(), c.Param("analysisID"))
	if err != nil {
		h.fail(c, err)
		return
	}

	// 运行中的扫描不导出
	//
	// 报告取自已落库的部分，运行中导出只会得到半成品，问题数还在涨、门禁没有结论，
	// 而文件名带时间戳，拿到的人无从判断这是过程中的快照
	//
	// 失败和取消的扫描照常导出，它们同样是有结论的记录，往往正是排查时要用的材料
	//
	// 用 ErrConflict 而不是 ErrInvalid，请求本身没问题，是资源当前状态不接受该操作，
	// classify 会把它映射成 409
	switch analysis.Status {
	case model.StatusPending, model.StatusRunning:
		h.fail(c, fmt.Errorf("%w: this scan is still running, wait for it to finish before exporting", service.ErrConflict))
		return
	}

	rep, err := h.Svc.BuildReport(c.Request.Context(), project.ID, analysis.ID)
	if err != nil {
		h.fail(c, err)
		return
	}

	// 文件名带上项目和扫描时间，导出的报告多半会和别的混在一个下载目录里，
	// 只写「报告」过两天就分不清是哪一份了
	name := fmt.Sprintf("%s-%s.%s",
		safeFilename(project.Name),
		analysis.CreatedAt.Format("20060102-150405"),
		format.Ext())
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))

	var buf bytes.Buffer
	switch format {
	case report.FormatJSON:
		err = report.WriteJSON(&buf, *rep)
	case report.FormatSARIF:
		err = report.WriteSARIF(&buf, *rep)
	default:
		err = report.WriteHTML(&buf, *rep, c.GetHeader("Accept-Language"))
	}
	if err != nil {
		h.fail(c, err)
		return
	}

	c.Data(http.StatusOK, format.ContentType(), buf.Bytes())
}

// safeFilename 把项目名收敛成可放进 Content-Disposition 的字符
//
// 项目名是用户输入，可能带引号、斜杠或换行，前两者会破坏响应头，后者会让头部解析
// 直接失败。中日韩字符保留，其余可疑字符换成连字符
func safeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r > 0x7f:
			// 非 ASCII 原样保留，Go 会按 UTF-8 写进头部，主流浏览器都能正确解析
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "report"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func (h *Handler) projectTrend(c *gin.Context) {
	project, err := h.Svc.ResolveProject(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}

	trend, err := h.Svc.Trend(c.Request.Context(), project.ID, queryInt(c, "limit", 30))
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, trend)
}

// ---------------------------------------------------------------------------
// 问题
// ---------------------------------------------------------------------------

func (h *Handler) listIssues(c *gin.Context) {
	analysisID := c.Param("id")

	limit := queryInt(c, "limit", 50)
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	filter := store.IssueFilter{
		Limit:   limit,
		Offset:  offset,
		OrderBy: c.DefaultQuery("orderBy", "severity"),
		File:    c.Query("file"),
		Search:  c.Query("search"),
	}
	// 未知的严重级别直接拒绝而不是归一，否则拼错会悄悄退化成 warning，
	// 返回一批过滤条件并未生效的结果
	for _, raw := range queryList(c, "severity") {
		sev, err := model.ParseSeverityStrict(raw)
		if err != nil {
			h.badRequest(c, err.Error())
			return
		}
		filter.Severities = append(filter.Severities, sev)
	}
	filter.Analyzers = queryList(c, "analyzer")
	filter.Rules = queryList(c, "rule")
	filter.Statuses = queryList(c, "status")
	for _, status := range filter.Statuses {
		switch status {
		case model.IssueStatusOpen, model.IssueStatusIgnored, model.IssueStatusFalsePositive:
		default:
			h.badRequest(c, fmt.Sprintf(
				"unknown status %q (valid: %s, %s, %s)",
				status, model.IssueStatusOpen, model.IssueStatusIgnored, model.IssueStatusFalsePositive))
			return
		}
	}
	filter.NewOnly = queryBool(c, "newOnly")
	if len(filter.Statuses) == 0 {
		// 默认只展示尚未分诊处理的发现
		filter.Statuses = []string{model.IssueStatusOpen}
	}

	issues, total, err := h.Svc.ListIssues(c.Request.Context(), analysisID, filter)
	if err != nil {
		h.fail(c, err)
		return
	}
	okList(c, issues, total, limit, offset)
}

func (h *Handler) getIssue(c *gin.Context) {
	issue, err := h.Svc.GetIssue(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, issue)
}

func (h *Handler) updateIssueStatus(c *gin.Context) {
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		h.badRequest(c, "status is required")
		return
	}

	issue, err := h.Svc.SetIssueStatus(c.Request.Context(), c.Param("id"), body.Status)
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, issue)
}

func (h *Handler) analysisStats(c *gin.Context) {
	stats, err := h.Svc.StatsForAnalysis(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, stats)
}

// ---------------------------------------------------------------------------
// 规则
// ---------------------------------------------------------------------------

func (h *Handler) listRules(c *gin.Context) {
	rules, err := h.Svc.ListRules(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, rules)
}

func (h *Handler) createRule(c *gin.Context) {
	var in service.RuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		h.badRequest(c, "invalid request body: "+err.Error())
		return
	}

	rule, err := h.Svc.CreateRule(c.Request.Context(), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	created(c, rule)
}

func (h *Handler) updateRule(c *gin.Context) {
	var in service.RuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		h.badRequest(c, "invalid request body: "+err.Error())
		return
	}

	rule, err := h.Svc.UpdateRule(c.Request.Context(), c.Param("id"), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, rule)
}

func (h *Handler) deleteRule(c *gin.Context) {
	if err := h.Svc.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(204)
}

func (h *Handler) toggleRule(c *gin.Context) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		h.badRequest(c, "enabled is required")
		return
	}

	rule, err := h.Svc.ToggleRule(c.Request.Context(), c.Param("id"), *body.Enabled)
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, rule)
}

// ---------------------------------------------------------------------------
// 质量门禁
// ---------------------------------------------------------------------------

func (h *Handler) getGate(c *gin.Context) {
	project, err := h.Svc.ResolveProject(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}

	gate, err := h.Svc.GetGate(c.Request.Context(), project.ID)
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, gate)
}

// scanPreflight 报告扫描启动前值得用户知道的事，哪些分析器跑不了、为什么
//
// 面板在按下「立即扫描」之后、真正入队之前调它。Blocked 为空表示一切就绪，
// 界面可以直接开跑而不必打扰用户
func (h *Handler) scanPreflight(c *gin.Context) {
	pre, err := h.Svc.ScanPreflight(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, pre)
}

func (h *Handler) saveGate(c *gin.Context) {
	project, err := h.Svc.ResolveProject(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}

	var in model.QualityGate
	if err := c.ShouldBindJSON(&in); err != nil {
		h.badRequest(c, "invalid request body: "+err.Error())
		return
	}

	gate, err := h.Svc.SaveGate(c.Request.Context(), project.ID, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	ok(c, gate)
}

// ---------------------------------------------------------------------------
// 系统
// ---------------------------------------------------------------------------

func (h *Handler) systemInfo(c *gin.Context) {
	ok(c, h.Svc.Info(c.Request.Context()))
}

func (h *Handler) listAnalyzers(c *gin.Context) {
	analyzerReadiness := h.Svc.Info(c.Request.Context()).Analyzers
	ok(c, analyzerReadiness)
}

// setOffline 处理 PUT /api/v1/system/offline
//
// 请求体用指针是为了区分「字段缺失」和「显式 false」。把缺席的 enabled 当成
// 「关掉它」，一个畸形请求就会静默重新开启网络访问
func (h *Handler) setOffline(c *gin.Context) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		h.fail(c, fmt.Errorf("%w: enabled is required", service.ErrInvalid))
		return
	}

	h.Svc.SetOffline(c.Request.Context(), *body.Enabled)
	ok(c, gin.H{"offline": *body.Enabled})
}

// setAnalyzerOptions 处理 PUT /api/v1/system/analyzers/:name/options
//
// 请求体是设置名到值的扁平映射，替换该分析器已存的全部设置。替换而不是合并是有意
// 的，合并式更新永远无法把选项重新关掉，缺席的键会保留旧值
func (h *Handler) setAnalyzerOptions(c *gin.Context) {
	name := c.Param("name")

	var body map[string]string
	if err := c.ShouldBindJSON(&body); err != nil {
		h.fail(c, fmt.Errorf("%w: expected an object of setting names to values", service.ErrInvalid))
		return
	}

	if err := h.Svc.SetAnalyzerOptions(c.Request.Context(), name, body); err != nil {
		h.fail(c, err)
		return
	}
	ok(c, h.Svc.Info(c.Request.Context()).AnalyzerOptions)
}

// healthz 供容器和 CI 的就绪探针使用
func (h *Handler) healthz(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

func isQueueFull(err error) bool {
	return err != nil && (containsAny(err.Error(), "queue is full", "shutting down"))
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
