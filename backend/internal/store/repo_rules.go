package store

import (
	"time"

	"gorm.io/gorm/clause"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// ---------------------------------------------------------------------------
// 规则
// ---------------------------------------------------------------------------

type ruleRepo struct{ db *DB }

func (r *ruleRepo) Create(rule *model.Rule) error {
	return translateError(r.db.Create(rule).Error)
}

func (r *ruleRepo) Update(rule *model.Rule) error {
	rule.UpdatedAt = time.Now()
	return translateError(r.db.Save(rule).Error)
}

func (r *ruleRepo) Delete(id string) error {
	return translateError(r.db.Delete(&model.Rule{}, "id = ?", id).Error)
}

func (r *ruleRepo) Get(id string) (*model.Rule, error) {
	var rule model.Rule
	if err := r.db.First(&rule, "id = ?", id).Error; err != nil {
		return nil, translateError(err)
	}
	return &rule, nil
}

func (r *ruleRepo) GetByName(name string) (*model.Rule, error) {
	var rule model.Rule
	if err := r.db.First(&rule, "name = ?", name).Error; err != nil {
		return nil, translateError(err)
	}
	return &rule, nil
}

func (r *ruleRepo) List() ([]model.Rule, error) {
	var out []model.Rule
	if err := r.db.Order("builtin DESC, name ASC").Find(&out).Error; err != nil {
		return nil, translateError(err)
	}
	return out, nil
}

func (r *ruleRepo) ListEnabled() ([]model.Rule, error) {
	var out []model.Rule
	if err := r.db.Where("enabled = ?", true).Order("name ASC").Find(&out).Error; err != nil {
		return nil, translateError(err)
	}
	return out, nil
}

// UpsertByName 同步由代码持有的规则定义，不会覆盖用户的启用状态或严重级别设置
//
// 被刷新的列是代码自己持有的那些，模式、它们的作用域，以及修复示例。把 fix 也算进
// 去，是为了让已有的数据库在升级后能拿到新的示例，没有它，那个专门告诉用户如何修复
// 发现的功能就只能出现在全新安装上
func (r *ruleRepo) UpsertByName(rule *model.Rule) error {
	return translateError(r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"patterns", "paths", "exclude", "fix", "message", "advice"}),
	}).Create(rule).Error)
}

func (r *ruleRepo) Count() (int64, error) {
	var n int64
	if err := r.db.Model(&model.Rule{}).Count(&n).Error; err != nil {
		return 0, translateError(err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// 质量门禁
// ---------------------------------------------------------------------------

type qualityGateRepo struct{ db *DB }

func (r *qualityGateRepo) Get(projectID string) (*model.QualityGate, error) {
	var g model.QualityGate
	if err := r.db.First(&g, "project_id = ?", projectID).Error; err != nil {
		return nil, translateError(err)
	}
	return &g, nil
}

func (r *qualityGateRepo) Save(g *model.QualityGate) error {
	g.UpdatedAt = time.Now()
	return translateError(r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"min_score", "max_critical", "max_high", "max_complexity", "min_coverage", "fail_on_warning", "enabled", "updated_at"}),
	}).Create(g).Error)
}

func (r *qualityGateRepo) Delete(projectID string) error {
	return translateError(r.db.Delete(&model.QualityGate{}, "project_id = ?", projectID).Error)
}
